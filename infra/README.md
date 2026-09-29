# infra

Terraform for GCP hosting. **Not written yet.**

Planned resources:

- Artifact Registry repository for the server image (built from the root `Dockerfile`)
- Cloud Run service running that image (`APP_ENV=prod`, `ADMIN_PASSWORD` from
  Secret Manager)
- The database, once chosen (see `docs/restructure-plan.md`)
- Domain mapping / load balancer for the custom domain
- CI (Cloud Build or GitHub Actions) to build and deploy on push to `main`

State and `*.tfvars` are gitignored; commit `*.tfvars.example` files instead.
