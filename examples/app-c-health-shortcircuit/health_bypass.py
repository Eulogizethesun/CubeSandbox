# Appended to the sandbox-code image's
# /opt/lightweight-code-interpreter/server.py at image build time.
#
# GET /health is answered by a bare ASGI callable before FastAPI sees it:
# no Starlette routing, dependency resolution, Pydantic serialization, CORS
# middleware or access log on this path. All other scopes (http non-/health,
# websocket, lifespan) are forwarded to the original FastAPI app unchanged.
#
# Note: server.py already starts with `from __future__ import annotations`;
# do not add another future import here, it must stay at the top of the file.


class _HealthBypass:
    def __init__(self, app):
        self.app = app

    async def __call__(self, scope, receive, send):
        if (
            scope.get("type") == "http"
            and scope.get("path") == "/health"
            and scope.get("method") == "GET"
        ):
            await send(
                {
                    "type": "http.response.start",
                    "status": 200,
                    "headers": [
                        (b"content-type", b"text/plain; charset=utf-8"),
                        (b"content-length", b"2"),
                    ],
                }
            )
            await send({"type": "http.response.body", "body": b"OK"})
            return
        await self.app(scope, receive, send)


app = _HealthBypass(app)
