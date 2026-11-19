# app-C: health short-circuit + C HTTP stack for `sandbox-code`

App-side optimization for CubeSandbox's `sandbox-code` image. It reduces the
**cold first `/health` probe after VM restore** without changing readiness
semantics: the probe still gets HTTP 200 `OK`.

This directory contains only the derived-image recipe; the stock application
source is not copied into this repository. The small ASGI bypass is appended to
the image's existing `server.py` at build time.

## Why

A restored VM keeps its guest RAM and process state, but the host's L1/L2/L3,
branch predictor and TLB are not part of the snapshot. The first HTTP request
after restore therefore pays cold i-cache/frontend stalls and vCPU exits. The
same request one moment later costs ~1ms. This is not Jupyter startup, page
faults or TCP handshake, and it cannot be removed by waiting or by warming a
snapshot before capture.

The optimization reduces the number of instructions that first request must
execute:

| Change | What it does | Why it helps | Caveat |
|---|---|---|---|
| `--no-access-log` | Disables uvicorn access logging | Skips `Logger.makeRecord` / `AccessFormatter` (about 2ms in py-spy samples) | **Proof of concept**: disables logs globally. Production should filter only `/health` and keep normal request logs. |
| Raw ASGI `/health` bypass | Answers `GET /health` before FastAPI sees it | Skips Starlette routing, FastAPI dependency resolution, Pydantic serialization, CORS and logs | Matches exactly `/health`; no CORS headers on this path; the FastAPI `/health` route becomes unreachable through the wrapper. |
| `--http httptools --loop uvloop` | C HTTP parser and libuv event loop | Replaces h11/asyncio on the cold path, smaller instruction footprint | Needs the wheels inside the image; pin versions. |

The bypass is a conditional early return, not extra processing: non-`/health`
requests pay one string comparison and are forwarded to FastAPI unchanged.
`lifespan` and `websocket` scopes are forwarded, so FastAPI startup/shutdown
and Jupyter-related websockets are unaffected.

## Readiness semantics

CubeSandbox treats the configured template probe as readiness: an HTTP GET to
`probePort` + `probePath` that must return a successful status code. The probe
does not inspect the body or validate FastAPI/CORS. This recipe preserves
`GET /health → 200 OK`.

If you also want a FastAPI/CORS smoke test, add a separate deep endpoint (for
example `/health/deep`) or a periodic functional test; do not rely on this
readiness endpoint for framework validation.

## Build

```bash
docker build \
  --build-arg BASE_IMAGE=cube-sandbox-cn.tencentcloudcr.com/cube-sandbox/sandbox-code:latest \
  -t app-c:local .
```

For a reproducible deployment, pin `BASE_IMAGE` by digest instead of using
`:latest`, and pin the `httptools` / `uvloop` versions.

## Use as a template

Create a template from the derived image with the same ports/probe as the
stock image, for example:

```bash
cubemastercli tpl create-from-image \
  --image <registry>/app-c:<tag> \
  --writable-layer-size 1G \
  --expose-port 49983 \
  --expose-port 49999 \
  --probe 49999 \
  --probe-path /health
```

`control` for A/B is the same base image without this recipe, with identical
CPU/memory/port/probe settings.

## Measured result (summary)

Code baseline: GitCode `boostkit/CubeSandbox` `v0.7.1` head `51f48102`.
Values are milliseconds, `control → app-C`, measured on an arm64 and an x86
test machine with different base images, so compare within a machine, not
across machines.

| Metric | arm64 | x86 |
|---|---:|---:|
| C1 create probe | 8.81 → 3.27 | 9.89 → 4.82 |
| C1 create wall | 52.40 → 44.08 | 55.19 → 49.73 |
| C1 resume probe | 24.45 → 9.62 | 26.06 → 16.90 |
| C100 create req avg | 95.918 → 91.856 | 86.279 → 78.908 |
| C10 resume wall avg | 58.556 → 29.490 | 91.774 → 54.212 |
| C10 resume probe | 116.210 → 28.662 | 281.311 → 54.287 |

Full data, concurrency analysis and caveats: see
[issue #40](https://github.com/Eulogizethesun/CubeSandbox/issues/40).

## Files

| File | Purpose |
|---|---|
| `Dockerfile` | Derived image: replace start script, append bypass, install httptools/uvloop |
| `start-http.sh` | uvicorn start script with the three flags |
| `health_bypass.py` | The ASGI wrapper appended to `server.py` |

## Notes

- This changes only the probe path. `execute`, `contexts` and Jupyter logic are
  untouched, but a functional regression test is still recommended.
- The `/health` bypass does not validate FastAPI routing or CORS; see the
  readiness section above.
- The access-log switch is a proof of concept; see the caveat in the table.
