# Deploy runbook

GCP project `benpriddycom`, region **`us-central1`**. (`us-east1` was tried
first; Cloud Run revisions there never became ready in this project, even for
Google's sample image, so everything lives in `us-central1`.)

## What's running

| Resource | Name | Notes |
|---|---|---|
| Cloud Run | `site` | main site; image `server`; SA `site-main`; Cloud SQL attached |
| Cloud Run | `usercontent` | front-end files; image `usercontent`; SA `site-usercontent` |
| Cloud SQL | `site-pg` | Postgres 17, `db-f1-micro`, 10 GB SSD, daily backups (7 kept); database `site`, user `site` |
| Artifact Registry | `us-central1-docker.pkg.dev/benpriddycom/site` | `server:<sha>`, `usercontent:<sha>` |
| Cloud Build | SA `site-build` | may only push to the repo and write logs |
| Secret Manager | `frontend-signing-key`, `admin-password`, `database-url`, `anthropic-api-key-never` | generated in place; never printed |
| Budget | "benpriddycom monthly" | $30/month; emails at 50/90/100% |
| Domain mappings | `benpriddy.com`, `www.benpriddy.com` → `site`; `benpriddy-usercontent.com` → `usercontent` | `www` redirects to the apex in the app |

Least privilege:

- `site-main` can read `frontend-signing-key`, `admin-password` and
  `database-url`, and connect to Cloud SQL.
- `site-usercontent` can read only `frontend-signing-key`. It has no database
  or admin access.

## Ship a change

```sh
G=gcloud   # or the full path to the SDK's gcloud
TAG=$(git rev-parse --short HEAD)
$G builds submit --config cloudbuild.yaml --project benpriddycom --region us-central1 \
  --service-account projects/benpriddycom/serviceAccounts/site-build@benpriddycom.iam.gserviceaccount.com \
  --substitutions _TAG=$TAG --async
# wait: gcloud builds list --project benpriddycom --region us-central1
$G run deploy site        --project benpriddycom --region us-central1 --image us-central1-docker.pkg.dev/benpriddycom/site/server:$TAG
$G run deploy usercontent --project benpriddycom --region us-central1 --image us-central1-docker.pkg.dev/benpriddycom/site/usercontent:$TAG
```

- `--async` is required. With a custom build service account, gcloud can't
  stream the logs and reports a misleading PERMISSION_DENIED.
- Deploying the image alone keeps each service's env, secrets, service account
  and Cloud SQL settings.
- Migrations run automatically when `site` starts (under an advisory lock).

## Service settings (for recreating from scratch)

```sh
# user-content
--service-account site-usercontent@benpriddycom.iam.gserviceaccount.com --allow-unauthenticated
--set-env-vars APP_ENV=prod,MAIN_ORIGIN=https://benpriddy.com,USERCONTENT_ORIGIN=https://benpriddy-usercontent.com
--set-secrets FRONTEND_SIGNING_KEY=frontend-signing-key:latest
--cpu 1 --memory 256Mi --min-instances 0 --max-instances 4

# main site
--service-account site-main@benpriddycom.iam.gserviceaccount.com --allow-unauthenticated
--add-cloudsql-instances benpriddycom:us-central1:site-pg
--set-env-vars APP_ENV=prod,MAIN_ORIGIN=https://benpriddy.com,USERCONTENT_ORIGIN=https://benpriddy-usercontent.com
--set-secrets FRONTEND_SIGNING_KEY=frontend-signing-key:latest,ADMIN_PASSWORD=admin-password:latest,DATABASE_URL=database-url:latest
--cpu 1 --memory 512Mi --min-instances 0 --max-instances 4
```

`database-url` has the form
`host=/cloudsql/benpriddycom:us-central1:site-pg dbname=site user=site password=… sslmode=disable`.
Cloud Run's built-in Cloud SQL connector provides the unix socket.

## Admin login

The user is `admin`. The password is the secret `admin-password`: in the
Cloud Console, open Secret Manager, then `admin-password`, then the latest
version, then View secret value. Or from a terminal:
`gcloud secrets versions access latest --secret admin-password --project benpriddycom`.

## Gotchas

- Cloud Run reserves URL paths ending in `z` (`/healthz` returns Google's 404),
  so the health check is `/health`.
- The front ends trust exactly one parent origin (`MAIN_ORIGIN`). Opened on the
  `*.run.app` URL, the main site loads, but the iframe is refused and the page
  falls back to the HTML transcript. That's expected; use the real domain.
- Domain-mapping TLS certificates are only issued once DNS points at Google;
  expect 15–60 minutes after the DNS change.

## Load balancer (built 2026-09-30, DNS not yet switched)

A global external Application Load Balancer, the long-term replacement for
the Cloud Run domain mappings (a preview feature, and slow to issue
certificates):

| Piece | Name |
|---|---|
| IPs | `site-lb-ip` 136.81.164.119, `site-lb-ipv6` 2600:1901:0:8020:: |
| Certificates (Certificate Manager, DNS-authorized) | `site-cert` (benpriddy.com, *.benpriddy.com), `uc-cert` (benpriddy-usercontent.com), map `site-cert-map` |
| Backends | `site-backend` → NEG `site-neg` → Cloud Run `site`; `usercontent-backend` → `usercontent-neg` → `usercontent` |
| Routing | URL map `site-urlmap`, by host; `site-http-redirect` sends port 80 to HTTPS |
| Frontends | forwarding rules `site-https-v4/v6` (443) and `site-http-v4/v6` (80) |

DNS authorization records (CNAMEs at GoDaddy, needed for certificate issuance
and harmless to live traffic):

- `_acme-challenge.benpriddy.com` →
  `61053e2e-b37c-4bb9-8220-dfd7ea89f569.8.authorize.certificatemanager.goog.`
- `_acme-challenge.benpriddy-usercontent.com` →
  `c4ef7035-e1fc-45f3-8122-3c405ca6779e.3.authorize.certificatemanager.goog.`

Cutover, once both certificates are ACTIVE and the LB is tested by IP:

1. Replace the Google A/AAAA records on both apexes with `136.81.164.119` /
   `2600:1901:0:8020::`.
2. Change `www` to an A record `136.81.164.119` plus AAAA
   `2600:1901:0:8020::` (the certificate covers `*.benpriddy.com`).
3. After DNS settles, delete the three Cloud Run domain mappings.
4. Set both services to `--ingress internal-and-cloud-load-balancing`, so they
   can only be reached through the LB.
5. Set `OBSERVE_XFF_HOPS=2` on `site`. Behind the LB the client IP is one hop
   further left in `X-Forwarded-For`; without this, every visitor shares one
   observer rate-limit bucket.
