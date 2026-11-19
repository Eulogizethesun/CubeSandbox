# syntax=docker/dockerfile:1.7
#
# app-C: reduce the cold first /health probe after VM restore.
#
# The recipe does NOT redistribute the sandbox-code application source: it
# starts from the stock image and appends the small ASGI bypass at build time.
#
# Build:
#   docker build \
#     --build-arg BASE_IMAGE=cube-sandbox-cn.tencentcloudcr.com/cube-sandbox/sandbox-code:latest \
#     -t app-c:local .
#
# Note: the --no-access-log flag in start-http.sh is a proof-of-concept switch
# that disables access logs globally. For production, keep access logs and
# filter only /health (see README).
ARG BASE_IMAGE=cube-sandbox-cn.tencentcloudcr.com/cube-sandbox/sandbox-code:latest
FROM ${BASE_IMAGE}

COPY start-http.sh /usr/local/bin/start-lightweight-code-interpreter.sh
COPY health_bypass.py /tmp/health_bypass.py

RUN chmod 755 /usr/local/bin/start-lightweight-code-interpreter.sh \
    && printf '\n\n' >> /opt/lightweight-code-interpreter/server.py \
    && cat /tmp/health_bypass.py >> /opt/lightweight-code-interpreter/server.py \
    && rm -f /tmp/health_bypass.py \
    && pip install --no-cache-dir httptools uvloop
