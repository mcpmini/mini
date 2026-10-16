# Logging in to MCPs

When `mini init` finishes, it lists each MCP that still needs a step:

| Line | Meaning | What to do |
|---|---|---|
| requires a login | The MCP supports browser login | Run `mini auth <name>` |
| requires a custom OAuth app | The MCP uses OAuth but won't let mini register itself | Register an OAuth app, or use a token if the provider offers one |
| requires manual configuration | The MCP needs a token, API key, or setting only you can supply | Edit the MCP's file |

Each MCP's file is `~/.mini/servers/<name>.yaml`. If you ran init with `--config DIR`, it's `DIR/servers/<name>.yaml`, and every `mini` command below needs the same `--config DIR`.

A running mini picks up each of these steps within a few seconds; see [Applying changes](#applying-changes).

## requires a login

```bash
mini auth linear
```

mini opens your browser to the provider's sign-in page and prints the URL in case the browser doesn't open. Once you approve, the browser returns to mini on `http://localhost:6464/callback` and mini saves the token in `~/.mini/internal/linear.token.json` (`DIR/internal/` with `--config DIR`). The login waits 5 minutes and prints `authorized linear` and the token's expiry when it's done. mini refreshes the token when it expires. If the refresh stops working, run `mini auth linear` again.

The login is tied to the MCP's URL, `client_id` and `token_url` as they were when you logged in. If you change any of them, mini stops sending the old token and asks you to run `mini auth linear` again.

## requires a custom OAuth app

Open the MCP's file first. If its comment says to create a token, as GitHub's does, follow [requires manual configuration](#requires-manual-configuration) instead; a token is quicker than an OAuth app.

If the comment holds OAuth app steps, they look like Asana's:

```yaml
# Register an OAuth app at https://app.asana.com/0/my-apps with redirect URI http://localhost:6464/callback, then uncomment:
# auth:
#     type: oauth2
#     client_id: <your app's client ID>
#     client_secret: <your app's client secret>  # leave this line out if your app has none

transport: http
url: https://mcp.asana.com/v2/mcp
```

1. In the provider's developer settings, create an OAuth app. Register the redirect URI `http://localhost:6464/callback` exactly. Follow the provider's MCP setup guide: if it offers an app type for MCP clients, such as Asana's MCP app, pick it. If the provider issues a client secret, keep it. Enable the scopes the MCP's documentation lists.
2. Uncomment the `auth:` block and replace both placeholders, or delete the `client_secret` line if your app has no secret. If the file has no comment, add `client_id` (and `client_secret`) to its `auth:` block, or add the whole block if there's none; a file can have only one `auth:` key. Leave the rest of the file as it is:

   ```yaml
   auth:
       type: oauth2
       client_id: 1209876543210
       client_secret: ${ASANA_CLIENT_SECRET}
   ```

   `client_secret` can reference an environment variable; set it first (see [Environment variables](#environment-variables)).
3. Log in:

   ```bash
   mini auth asana
   ```

mini requests the scopes the MCP advertises. A list under `auth.scopes` replaces them, so include every scope you need. If port 6464 is taken, set `auth.callback_port` and register that port in the redirect URI instead. If the provider rejects the redirect URI, compare the app's registered URI with the `redirect_uri` in the URL `mini auth` prints, port included.

## requires manual configuration

Open the MCP's file. For an MCP from the catalog that needs a token, init already wrote where to get one and the block to uncomment. GitHub's file, for example:

```yaml
# Create a token at https://github.com/settings/personal-access-tokens, then uncomment:
# headers:
#     Authorization: Bearer <your token>

transport: http
url: https://api.githubcopilot.com/mcp/
```

Create the token, uncomment the block, and replace `<your token>`. For GitHub, grant the token the repositories and permissions the tools you'll use need; a token with too little access connects, but its calls fail with 403 or 404.

Reference an environment variable rather than pasting the token into the file:

```yaml
headers:
    Authorization: Bearer ${GITHUB_TOKEN}
```

The value is sent as written. Most providers want the `Bearer ` prefix, but not all: PagerDuty wants `Authorization: Token token=${PAGERDUTY_API_KEY}` for an API key. Check the provider's MCP documentation for the value it expects. For a `Bearer ` token, an `auth:` block with `type: bearer` and `token: ${GITHUB_TOKEN}` does the same and adds the prefix for you.

For a provider that wants a different header, such as `X-API-Key`, use an `auth:` block with `type: apikey`, which sends the token as-is in `header`. Put it under `auth:` rather than `headers:`: if the MCP asked for OAuth earlier, mini keeps offering an OAuth login unless the file has an `Authorization` header or an `auth:` block of another type. Replace any `auth:` block the file already has; a file can have only one.

```yaml
auth:
    type: apikey
    header: X-API-Key
    token: ${ACME_API_KEY}
```

An MCP that runs as a local command takes its credentials as environment variables. `env:` is a list of `NAME=value` entries:

```yaml
command: npx
args: ["-y", "acme-mcp"]
env:
  - ACME_API_KEY=${ACME_API_KEY}
```

The command already inherits mini's environment. Listing the variable under `env:` makes mini report it by name when it isn't set, instead of starting a command that fails.

If the file has no setup comment, run `mini status`. Its error names any variable that isn't set. If none is missing, the MCP turned mini away without asking for OAuth, and its documentation says which header or variable it expects.

If you'd rather not set up an MCP now, remove it with `mini rm <name>`.

### Environment variables

`${NAME}` is expanded in `headers`, `env`, `auth.token` and `auth.client_secret`. A `${NAME}` in `url`, `command` or `args` is an error, and mini won't load the file. `auth.client_id` and the other `auth` fields take the text as written.

If a variable isn't set, mini doesn't connect to that MCP; it doesn't send the literal `${NAME}`.

mini takes its environment from the agent that starts it, and every agent shares that one mini. To add a variable:

1. Export it in your shell profile and open a new terminal.
2. Restart mini as in [Applying changes](#applying-changes), starting your first agent from that terminal.

An agent started from the Dock or a desktop launcher usually doesn't see variables from your shell profile. Start it from a terminal, or write the value into the file.

A literal value in the file works too. init writes server files readable only by you, but they're plain text, so keep them out of anything you share or commit.

## Applying changes

A running mini checks the files in `servers/` every 5 seconds. When you add, edit or remove an MCP's file, or `mini auth` or `mini add` saves a login, it connects, reconnects or drops that MCP and tells your agents their tool list changed. If an agent doesn't show the change, restart that agent.

mini keeps the environment it started with, and it reads `config.yaml` at startup. After changing an environment variable or `config.yaml`, restart mini:

1. Quit every agent that uses mini.
2. Stop mini's background process, if one is running:

   ```bash
   pkill -f 'mini.* daemon$'
   ```

   `mini daemon status` shows whether it's still running.
3. Start your agents again. The first one starts mini with the new settings.

You don't need to run `mini init` again.

## Checking it works

```bash
mini status
```

`mini status` connects to each MCP and lists the result:

```
NAME    TRANSPORT  STATUS                                                                          TOOLS
github  http       ok                                                                              41
linear  http       error: list tools from linear: linear is not logged in; run `mini auth linear`  -
```

It exits non-zero if any MCP fails. It runs in your terminal, so it sees your terminal's environment variables; an MCP can be `ok` here and still fail in an agent whose mini started without them.

Common errors:

- **`` <name> is not logged in; run `mini auth <name>` ``** or **`` <name> requires OAuth authorization; run `mini auth <name>` ``**: run `mini auth <name>`.
- **`` <name> requires re-authorization; run `mini auth <name>` ``**: the saved login expired and couldn't be refreshed. Run `mini auth <name>`.
- **`unauthorized (WWW-Authenticate: ...)`**: the MCP rejected the token or key in the file, or the file has none and the MCP needs one. Check the value, or see [requires manual configuration](#requires-manual-configuration).
- **`<field>: NAME isn't set where mini runs`** (or `aren't set` for several): see [Environment variables](#environment-variables).
- **`<field>: ${NAME} isn't expanded`**: `${...}` was used in a field that doesn't expand it. See [Environment variables](#environment-variables).

If `mini auth <name>` fails with **no client_id configured and server provides no registration endpoint**, the MCP needs a custom OAuth app. See [requires a custom OAuth app](#requires-a-custom-oauth-app). If it fails with **the server only accepts mini's client metadata document, which lists callback port 6464**, remove `auth.callback_port` or set up your own app.

## How mini decides

If the MCP asks for OAuth, mini reads its authorization server's metadata:

- If the server lets clients register themselves (client ID metadata documents or dynamic client registration), mini can log in by itself: **requires a login**. Client ID metadata documents only work on the default callback port, 6464, so with `auth.callback_port` set to another port, mini needs dynamic client registration.
- If it doesn't, the provider only accepts apps registered by hand: **requires a custom OAuth app**. The same line shows while the file still holds `<your app's client ID>` or `<your app's client secret>`.

**requires manual configuration** means one of:

- The file references an environment variable that isn't set, such as `${GITHUB_TOKEN}`.
- A header still holds the placeholder init wrote, `<your token>`.
- The MCP refused the connection without asking for OAuth, so it wants a credential mini can't get for you.
