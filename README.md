# kronk-chat

A small local web chat built on [Kronk](https://github.com/ardanlabs/kronk).
It streams answers, can write documents for you, and turns speech into text.
Everything runs on your machine.

## Requirements

- Go 1.27
- Docker (only for the Docker setup)

## Configure

Edit `config.yml`:

```yaml
model: unsloth/Qwen3-0.6B-Q8_0   # chat model
speech_model: base.en            # whisper model for the mic button
kronk_dir: ~/.kronk              # where libraries and models are stored
```

## Run on your machine

```sh
make run
```

Open http://localhost:8080

The first start downloads the libraries and models (about 800 MB).
Later starts use the files in `kronk_dir`.

## Download models only

Fill `kronk_dir` without starting the server:

```sh
go build -o kronkchat . && ./kronkchat -download
```

## Run in Docker

Build the image. Models are copied from `kronk_dir` and only downloaded if they are missing:

```sh
make docker
```

Start it:

```sh
make runDocker
```

Open http://localhost:8080

The container needs no network. It runs on the CPU, so it is slower than `make run`.

Use another port or sessions folder:

```sh
make runDocker PORT=9090 SESSIONS=$HOME/kronk-sessions
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

Cache the Kronk folder and pass it in:

```sh
make docker KRONK_DIR=/path/to/cache/kronk
```

A warm cache means no model downloads.
