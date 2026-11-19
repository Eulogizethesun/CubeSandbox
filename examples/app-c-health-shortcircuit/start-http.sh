#!/bin/sh
set -eu

ENVD_PORT="${ENVD_PORT:-49983}"
CODE_INTERPRETER_HOST="${CODE_INTERPRETER_HOST:-0.0.0.0}"
CODE_INTERPRETER_PORT="${CODE_INTERPRETER_PORT:-49999}"
CODE_INTERPRETER_WORKDIR="${CODE_INTERPRETER_WORKDIR:-/workspace}"
JUPYTER_HOST="${JUPYTER_HOST:-127.0.0.1}"
JUPYTER_PORT="${JUPYTER_PORT:-8888}"

mkdir -p "$CODE_INTERPRETER_WORKDIR"

/usr/bin/envd -port "$ENVD_PORT" >/var/log/envd.log 2>&1 &
echo "[lightweight-code-interpreter] envd started on :$ENVD_PORT"

JUPYTER_BASE_URL="http://$JUPYTER_HOST:$JUPYTER_PORT"
export JUPYTER_BASE_URL

jupyter server \
  --IdentityProvider.token="" \
  --ServerApp.ip="$JUPYTER_HOST" \
  --ServerApp.port="$JUPYTER_PORT" \
  --ServerApp.root_dir="$CODE_INTERPRETER_WORKDIR" \
  --ServerApp.allow_root=True \
  --ServerApp.disable_check_xsrf=True \
  --ServerApp.open_browser=False \
  >/var/log/jupyter.log 2>&1 &
echo "[lightweight-code-interpreter] Jupyter Server started on $JUPYTER_BASE_URL"

exec python -m uvicorn \
  --host "$CODE_INTERPRETER_HOST" \
  --port "$CODE_INTERPRETER_PORT" \
  --log-level info \
  --no-access-log \
  --http httptools \
  --loop uvloop \
  --timeout-keep-alive 640 \
  server:app \
  --app-dir /opt/lightweight-code-interpreter
