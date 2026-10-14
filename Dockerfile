# Build with `make docker [CONFIG=...]`: it stages the config into
# .docker/config.yml and the libraries and models it uses from your kronk
# dir into .docker/kronk, so this build downloads no models.

# ---- build: a static, CGO-free binary (Kronk loads its native libs via purego)
FROM golang:1.27-trixie AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/kronkchat .

# ---- runtime: glibc base for the llama.cpp / whisper.cpp shared libraries.
# libffi is loaded at runtime by Kronk's FFI layer; libgomp by ggml's CPU backend.
FROM debian:trixie-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates libffi8 libgomp1 \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --create-home --uid 1000 app \
 && mkdir -p /opt/kronk /data/sessions \
 && chown app:app /opt/kronk /data/sessions

# Libraries and models first: they are big and change rarely, so this layer
# stays cached while the app is rebuilt.
COPY --chown=app:app .docker/kronk /opt/kronk

WORKDIR /app
COPY --from=build /out/kronkchat /app/kronkchat
COPY .docker/config.yml /app/config.yml

USER app

# Index the copied models and check nothing is missing, without network.
RUN /app/kronkchat -kronk-dir /opt/kronk -offline -download

# Sessions live outside the image; mount a volume here.
VOLUME /data/sessions
EXPOSE 8080

ENTRYPOINT ["/app/kronkchat", "-kronk-dir", "/opt/kronk", "-offline", "-data", "/data/sessions", "-addr", "0.0.0.0:8080"]
