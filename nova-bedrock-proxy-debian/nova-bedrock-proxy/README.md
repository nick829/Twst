# Nova Bedrock Proxy (Go)

This is a Go source project for a Bedrock protocol proxy using Gophertunnel. This archive includes a non-root Go bootstrap for Debian-based containers that do not have Go preinstalled.

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

## Important limitations

- This source has not been compiled or integration-tested in your Spell/FeatherPanel instance. If it fails, share the complete build/runtime error.
- If the Debian image lacks `curl` or `tar`, the installer will fail; use an image with these tools or ask the panel administrator to include them.
- The proxy does not inject arbitrary uploaded resource packs into a third-party server. The destination server controls its resource-pack negotiation.
