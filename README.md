# Nova Bedrock Proxy

NovaBedrockProxy is a Bedrock Edition proxy using Gophertunnel over RakNet/UDP. FeatherPanel runs the prebuilt Linux AMD64 executable; Go is needed only to build the project, not on the game server.

## FeatherPanel setup

1. Download the `NovaBedrockProxy-Linux-AMD64` artifact from a successful run of the `Build NovaBedrockProxy` GitHub Actions workflow.
2. Extract and upload the executable named `server` to `/home/container`, replacing the existing `server`.
3. Allocate a UDP port in FeatherPanel and configure the Startup Variables below.
4. Use this exact Startup Command:

   ```sh
   chmod +x ./server && exec ./server
   ```

The workflow only creates a downloadable artifact; it does not deploy or replace files in FeatherPanel automatically. After a later build, download the new artifact and manually replace `/home/container/server`.

## Startup Variables

| Variable | Default | Description |
| --- | --- | --- |
| `LISTEN_ADDR` | `0.0.0.0:25665` | Local Bedrock UDP listen address. Set the port to FeatherPanel's allocated UDP port. |
| `REMOTE_ADDR` | `summitsurvival.org:63809` | Upstream Bedrock server as `host:port`. Set this in FeatherPanel to change the destination. |
| `RESOURCE_PACK_DIR` | `resource_packs` | Pack directory relative to the executable, or an absolute path. |
| `AUTH_TOKEN_FILE` | `.novabedrock-auth.json` | Persistent Microsoft/Xbox token file relative to the executable, or an absolute path. |
| `COMMAND_PREFIX` | `.` | Prefix for local proxy chat commands. |
| `FORWARD_UNKNOWN_PREFIX_COMMANDS` | `true` | Forward unrecognized prefix commands to the upstream server. |
| `SCANNER_ENABLED` | `true` | Enable bounded packet metadata diagnostics. |
| `SCANNER_CACHE_LIMIT` | `256` | Recent scanner entries to retain, from `0` to `4096`. |

The default pack directory resolves next to `server`, which is `/home/container/resource_packs` in the normal FeatherPanel layout. The proxy creates it on startup. Place any number of valid `.mcpack` or supported `.zip` resource packs there; valid packs are offered to connecting Bedrock clients through Gophertunnel. Invalid packs are logged and skipped. Packs are discovered again each time the proxy starts. If there are no valid packs, normal proxy operation continues without requiring packs.

The first run prints a Microsoft device-code sign-in prompt in the server console. The resulting refresh token is persisted locally with owner-only file permissions and reused/refreshed on later runs. Do not share `.novabedrock-auth.json` or upload it as an artifact. If Microsoft/Xbox credentials have expired or been revoked, the proxy reports that a new device-code sign-in is needed. The default token file is ignored by Git.

The proxy resolves the upstream hostname for each incoming player connection. DNS failures, UDP timeouts, and refused/rejected UDP connections are reported separately; a temporary upstream outage does not stop the listener. Logs do not include player IP addresses or authentication tokens. SIGTERM and Ctrl-C stop accepting players and close active relay connections.

The destination address is configurable; this project does not claim that a particular upstream server supports proxy connections.

## Existing proxy commands

Use `.help` in Bedrock chat or `help` in the FeatherPanel console to list local commands. `.packstatus` reports packs offered by this proxy, `.proxyinfo` reports the configured addresses and current connection count, and `.scanner status` / `.scanner reset` manage the existing bounded packet diagnostics.

Scanner diagnostics count only `LevelChunk`, `UpdateBlock`, `PlayerAuthInput`, and `MovePlayer` packets observed on active proxy connections. They do not decode chunk payloads, retain positions or block identifiers, or inspect data the upstream server has not sent to a connected player.
