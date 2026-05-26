# Fly.io Deployment

This app should run as a single always-on Fly Machine with one attached volume.
That matters because the app keeps long-lived Steam/Dota bot sessions and writes
SQLite, queue state, and logs to disk.

## One-time setup

Install and log in with `flyctl`, then choose an app name:

```sh
fly auth login
fly apps create <app-name>
```

Edit `fly.toml`:

- Set `app` to `<app-name>`.
- Set `BASE_URL` to `https://<app-name>.fly.dev` or your custom domain. Steam OpenID callbacks depend on this matching the public URL.
- Keep `auto_stop_machines = "off"` and `min_machines_running = 1`.
- Keep only one Machine unless the app is moved off SQLite and bot ownership is made distributed.

Create the persistent volume in the same region as `primary_region`:

```sh
fly volumes create dota_inhouse_data --app <app-name> --region arn --size 1
```

If you choose a different primary region, use that same region for the volume.

## Runtime secrets

Set secrets from your current local environment. Do not commit these values.

```sh
fly secrets set --app <app-name> \
  STEAM_API_KEY="..." \
  ADMIN_STEAM_IDS="..." \
  BOT1_USERNAME="..." \
  BOT1_PASSWORD="..." \
  BOT2_USERNAME="..." \
  BOT2_PASSWORD="..." \
  BOT3_USERNAME="..." \
  BOT3_PASSWORD="..." \
  BOT4_USERNAME="..." \
  BOT4_PASSWORD="..." \
  VAPID_PUBLIC_KEY="..." \
  VAPID_PRIVATE_KEY="..." \
  VAPID_SUBJECT="mailto:you@example.com"
```

Optional non-secret settings can stay in `fly.toml` or be set as secrets if you
prefer operational changes without editing the file:

```sh
fly secrets set --app <app-name> MAX_PLAYERS="10" LEAGUE_ID="..."
```

## Deploy

```sh
fly deploy --app <app-name>
fly scale count 1 --app <app-name>
fly status --app <app-name>
fly logs --app <app-name>
```

## Migrating existing data

The app stores persistent files under `/data` on Fly:

- `/data/inhouse.db`
- `/data/queue.json`
- `/data/inhouse.log`

To seed the current SQLite database after the first deploy, stop the Machine,
upload the database, then start it again:

```sh
fly machine list --app <app-name>
fly machine stop <machine-id> --app <app-name>
fly ssh sftp put --app <app-name> data/inhouse.db /data/inhouse.db
fly machine start <machine-id> --app <app-name>
```

Check the mounted volume:

```sh
fly ssh console --app <app-name> -C "ls -lh /data"
```

## Notes

- Fly runtime secrets are injected as environment variables at boot.
- The Docker image copies `web/` because templates and static files are loaded from disk.
- The root filesystem is ephemeral; only `/data` persists across deploys and restarts.
- Volume snapshots are managed by Fly, but an occasional external SQLite backup is still wise.
