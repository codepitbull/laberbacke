# kronk-chat

A small local web chat built on [Kronk](https://github.com/ardanlabs/kronk).
It streams answers, can write documents for you, and turns speech into text.
Everything runs on your machine.

## Requirements

- Go 1.27
- Docker (only for the Docker setup)

## Configs

A config file picks the models:

```yaml
model: unsloth/Qwen3-0.6B-Q8_0   # chat model
speech_model: base.en            # whisper model for the mic button
kronk_dir: ~/.kronk              # where libraries and models are stored
```

| File | For | Download |
|---|---|---|
| `config.yml` (default) | small machines, Qwen3 0.6B | ~0.8 GB |
| `config_16gb.yml` | 16 GB VRAM, gpt-oss 20B | ~12.5 GB |

Pick one with `CONFIG=`. Without it, `config.yml` is used.

## Run on your machine

```sh
make run
```

```sh
make run CONFIG=config_16gb.yml
```

Open http://localhost:8080. Stop with `Ctrl+C`.

The first start downloads what the config needs into `kronk_dir`.
Later starts use those files.

## Download models only

Fill `kronk_dir` without starting the server:

```sh
go build -o kronkchat . && ./kronkchat -config config.yml -download
```

```sh
go build -o kronkchat . && ./kronkchat -config config_16gb.yml -download
```

## Run in Docker

The config is built into the image, so give each config its own image name.

Small config:

```sh
make docker
make runDocker
```

16 GB config:

```sh
make docker CONFIG=config_16gb.yml IMAGE=kronkchat:16gb
make runDocker IMAGE=kronkchat:16gb
```

Open http://localhost:8080. Stop with `Ctrl+C`.

- `make docker` copies only the models of that config from `kronk_dir` into the image. It downloads them only if they are missing.
- The container needs no network.
- It runs on the CPU, so it is slower than `make run`.
- Image size is about 1 GB (small config) or 12.5 GB (16 GB config). `docker image ls` can show about double, because it counts the packed and unpacked copy.
- The 16 GB image needs a Docker VM with at least 16 GB of memory.

## Make options

| Option | Default | Used by |
|---|---|---|
| `CONFIG` | `config.yml` | `run`, `docker` |
| `IMAGE` | `kronkchat` | `docker`, `runDocker` |
| `PORT` | `8080` | `run`, `runDocker` |
| `SESSIONS` | `./sessions` | `runDocker` |
| `KRONK_DIR` | `kronk_dir` from the config | `run`, `docker` |

Example:

```sh
make runDocker IMAGE=kronkchat:16gb PORT=9090 SESSIONS=$HOME/kronk-sessions
```

## Use it

- Type in the terminal pane and press Enter.
- Click the mic icon to record. Click it again to stop. The text appears in the prompt line.
- Ask for a file, for example "Create a document called notes.md with ...". It shows up on the right. Click it to download.
- Each chat has its own link (`/s/<uuid>`). Old chats are listed on the left. Click "+ new chat" to start a new one.

## Where data is stored

| What | Where |
|---|---|
| Chats and documents | `sessions/<uuid>/` (Docker: the `SESSIONS` folder) |
| Chat transcript | `sessions/<uuid>/chat.md` |
| Libraries and models | `kronk_dir` (Docker: inside the image) |

## CI

Cache the Kronk folder and pass it in. A warm cache means no model downloads.

```sh
make docker CONFIG=config_16gb.yml IMAGE=kronkchat:16gb KRONK_DIR=/path/to/cache/kronk
```

## Known issues

- gpt-oss (`config_16gb.yml`) thinks before it answers. The app does not show this yet, so the reply starts after a short pause.
