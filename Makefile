IMAGE     ?= kronkchat
PORT      ?= 8080
SESSIONS  ?= $(CURDIR)/sessions
# Kronk cache dir; empty uses kronk_dir from config.yml. Point CI caching here.
KRONK_DIR ?=

STAGE       := .docker/kronk
KRONK_FLAGS := $(if $(KRONK_DIR),-kronk-dir $(KRONK_DIR))

.PHONY: build run docker runDocker

build:
	go build -o kronkchat .

## run: run the app directly on this machine
run: build
	./kronkchat $(KRONK_FLAGS) -addr localhost:$(PORT)

## docker: build the image. Libraries and models come from the kronk dir
## (downloaded there only if missing); only the ones in use are copied in.
docker: build
	rm -rf $(STAGE)
	KRONK_OS=linux KRONK_ARCH=$$(docker version -f '{{.Server.Arch}}') KRONK_PROCESSOR=cpu \
		./kronkchat $(KRONK_FLAGS) -stage $(STAGE)
	docker build -t $(IMAGE) .

## runDocker: run the image; sessions are stored in $(SESSIONS) on the host.
## Runs as your uid/gid so the container can write to the mounted folder.
runDocker:
	mkdir -p $(SESSIONS)
	docker run --rm --init --user $$(id -u):$$(id -g) -p $(PORT):8080 \
		-v $(SESSIONS):/data/sessions $(IMAGE)
