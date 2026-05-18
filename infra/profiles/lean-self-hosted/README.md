# Lean self-hosted (single-node EC2)

The simplest deploy target: one EC2 instance, embedded NATS, self-hosted
Postgres, the whole docker-compose stack from this repo running under
systemd. Sized for the design's pilot footprint (~35 developers, one
pilot repo) and not for HA.

## What you get

- 1 EC2 instance (Graviton `t4g.medium` by default; flip `instance_arch`
  to `x86_64` if you'd rather)
- Default-VPC public subnet + security group
- IAM role with SSM Session Manager attached so SSH is optional
- cloud-init that installs Docker, writes the production compose file,
  and registers a `codereviewer.service` systemd unit
- `aws_instance.host` has `delete_on_termination = false` on the root
  volume — terraform destroy will not wipe Postgres

## Apply

```sh
cd infra/profiles/lean-self-hosted
cat > terraform.tfvars <<EOF
region        = "us-east-1"
name          = "codereviewer"
instance_type = "t4g.medium"
image_owner   = "ghcr.io/<your-github-user>/codereviewer"
image_tag     = "v0.5.0"
# Lock webhook ingress to GitHub's documented CIDRs if your security
# posture requires it.
webhook_ingress_cidrs = ["0.0.0.0/0"]
EOF
terraform init
terraform apply
```

## Finish the bootstrap

Drop secrets and config onto the host via SSM Session Manager — the
user-data script leaves `/opt/codereviewer/NEXT_STEPS.txt` with the
exact paths and chmods. Then:

```sh
sudo systemctl start codereviewer
sudo systemctl status codereviewer
```

## Connect to the admin UI

The admin UI binds to `127.0.0.1:8090` on the host (not exposed
publicly). Forward via SSM:

```sh
aws ssm start-session \
  --target $(terraform output -raw instance_id) \
  --document-name AWS-StartPortForwardingSession \
  --parameters '{"portNumber":["8090"],"localPortNumber":["8090"]}'
```

Then browse to `http://localhost:8090`.

## TLS termination

The compose file maps `:443` → `webhook-gateway:8080` raw HTTP. For
production deployments, front the gateway with Caddy or nginx on the
same host (Caddy can ACME-provision a Let's Encrypt cert against the
EC2 public DNS automatically). The OTLP exporter respects the
`[observability].insecure` config flag — flip to `false` once your
collector has a verified TLS cert.

## Why not Fargate / ECS / Kubernetes

This profile targets the lean-self-hosted footprint from `docs/design.md`.
A second profile (`infra/profiles/fargate/` or similar) is the right
place to add a managed-runtime variant; it's not in scope for slice 5.

## What this profile deliberately does NOT do

- TLS provisioning (bring your own Caddy/nginx, or front with ALB)
- Multi-AZ / HA (single instance, single AZ)
- Backups (use AWS Backup against the EBS volume, or `pg_dump` on a cron)
- Postgres tuning (default `pgvector/pgvector:pg16` settings)
- DNS (point an A record at `output.public_ip` yourself)
- WAF (security group is the only ingress filter)
