# Nova Bedrock Proxy (Go)

This is a Go source project for a Bedrock protocol proxy using Gophertunnel. This archive includes a non-root Go bootstrap for Debian-based containers that do not have Go preinstalled.

## Prebuilt Linux AMD64 executable

The repository's GitHub Actions workflow builds the real Go application when manually run or when code is pushed to `main`. To download it, open the successful workflow run on GitHub, select the `NovaBedrockProxy-Linux-AMD64` artifact, and upload the extracted `server` file to the FeatherPanel server root (usually `/home/container`).

Use this FeatherPanel Startup Command to launch the uploaded executable directly:

```sh
chmod +x /home/container/server && exec /home/container/server
```

Set `LISTEN_ADDR` to `0.0.0.0:<allocated-UDP-port>` and `REMOTE_ADDR` to `<destination-host>:<destination-port>` as needed. Defaults are `0.0.0.0:19132` and `donutsmp.net:19132`, respectively. The listen port must be allocated as UDP in FeatherPanel. On startup, Microsoft device authentication prints a URL and code in the server console; complete that sign-in flow when prompted.

## For `ghcr.io/ptero-eggs/yolks:debian`

The Debian image does not necessarily include Go. Do not use `go build ...` directly until Go has been installed.

1. Upload/extract this folder so `go.mod`, `main.go`, `install-go.sh`, and `start.sh` are in the server working directory (commonly `/home/container`).
2. Ensure `curl` and `tar` exist in the Debian image. The installer does not use `apt-get` or require root.
3. Run `bash ./install-go.sh` once from the Script Install process. It downloads Go 1.25.0 into `.tools/go`, downloads module dependencies, and builds `./server`.
4. Set Startup Command to `bash ./start.sh`.
5. Set Go Package to `.` if the egg requires it. The executable setting is not used by `start.sh`, because the script builds and launches `./server` itself.
6. Set environment variables if the panel supports them:
   - `LISTEN_ADDR=0.0.0.0:25649`
   - `REMOTE_ADDR=donutsmp.net:19132`
7. Ensure the allocated port is UDP; Bedrock uses UDP.

If Script Install cannot run shell scripts from the server directory, use its command field to run `bash ./install-go.sh` or paste that command into the install step. Do not enable privileged scripts just to install Go.

## Scanner packet diagnostics

The proxy provides `.scanner` and `.scanner status` in Minecraft chat and the FeatherPanel server console. Use `.scanner reset` to clear the in-memory counters and recent metadata cache. Console commands accept either `scanner status` or `.scanner status`. Chat commands recognized by the proxy are handled locally and are not forwarded upstream; ordinary chat and unrelated packets continue through the existing relay.

`SCANNER_ENABLED` defaults to `true`. Set it to `false` to disable packet tracking. `SCANNER_CACHE_LIMIT` defaults to `256` and accepts values from `0` to `4096`; `0` retains counters but no recent-event history. The cache stores a bounded number of packet-type/direction records and limited `LevelChunk` metadata such as sub-chunk count, serialized payload size, and cache mode.

Diagnostics count only `LevelChunk`, `UpdateBlock`, `PlayerAuthInput`, and `MovePlayer` packets observed on active proxy connections. They do not decode chunk payloads or block palettes, retain block/player coordinates, identify hidden blocks, or inspect data the upstream server has not sent to a connected player. This is packet-level diagnostics, not an X-ray or complete world scanner.

## Important limitations

- A successful compile does not verify connectivity or authentication in your FeatherPanel instance; check its logs if startup or upstream login fails.
- If the Debian image lacks `curl` or `tar`, the installer will fail; use an image with these tools or ask the panel administrator to include them.
- The proxy does not inject arbitrary uploaded resource packs into a third-party server. The destination server controls its resource-pack negotiation.
