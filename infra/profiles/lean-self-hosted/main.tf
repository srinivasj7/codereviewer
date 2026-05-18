# Single-node "lean self-hosted" profile.
#
# What it brings up: one EC2 instance with Docker installed, the
# docker-compose stack from this repo dropped into /opt/codereviewer,
# and systemd units that start the stack on boot. SSM Session Manager
# is enabled out of the box so SSH is optional.
#
# What it deliberately does NOT do: TLS termination (front with ALB
# or Caddy/nginx of your choosing), Postgres backup (use AWS Backup
# against the instance volume, or run pg_dump on a cron), or HA. The
# design's "lean self-hosted" target is exactly one instance.

locals {
  common_tags = merge(
    {
      Name        = var.name
      Application = "codereviewer"
      Profile     = "lean-self-hosted"
    },
    var.tags,
  )
}

# Default VPC lookup. Operators with a custom VPC should fork this module
# and replace these data lookups with their subnet/VPC IDs.
data "aws_vpc" "default" {
  default = true
}

data "aws_subnets" "default" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.default.id]
  }
}

data "aws_ami" "al2023" {
  most_recent = true
  owners      = ["amazon"]

  filter {
    name   = "name"
    values = ["al2023-ami-2023.*"]
  }

  filter {
    name   = "architecture"
    values = [var.instance_arch]
  }

  filter {
    name   = "root-device-type"
    values = ["ebs"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

# Security group: explicit allow per ingress class, all egress open.
resource "aws_security_group" "host" {
  name        = "${var.name}-host"
  description = "Inbound for webhook gateway, admin UI, optional SSH."
  vpc_id      = data.aws_vpc.default.id

  egress {
    description = "All outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = local.common_tags
}

resource "aws_security_group_rule" "webhook" {
  type              = "ingress"
  from_port         = 443
  to_port           = 443
  protocol          = "tcp"
  cidr_blocks       = var.webhook_ingress_cidrs
  security_group_id = aws_security_group.host.id
  description       = "GitHub webhook gateway (TLS terminated by reverse proxy on the host)."
}

resource "aws_security_group_rule" "webhook_http" {
  type              = "ingress"
  from_port         = 80
  to_port           = 80
  protocol          = "tcp"
  cidr_blocks       = var.webhook_ingress_cidrs
  security_group_id = aws_security_group.host.id
  description       = "HTTP for cert provisioning (ACME challenge); redirect to HTTPS once a cert is in place."
}

resource "aws_security_group_rule" "admin" {
  count             = length(var.admin_ingress_cidrs) > 0 ? 1 : 0
  type              = "ingress"
  from_port         = 8090
  to_port           = 8090
  protocol          = "tcp"
  cidr_blocks       = var.admin_ingress_cidrs
  security_group_id = aws_security_group.host.id
  description       = "Admin UI direct access. Prefer connecting via SSM port-forward."
}

resource "aws_security_group_rule" "ssh" {
  count             = length(var.ssh_ingress_cidrs) > 0 ? 1 : 0
  type              = "ingress"
  from_port         = 22
  to_port           = 22
  protocol          = "tcp"
  cidr_blocks       = var.ssh_ingress_cidrs
  security_group_id = aws_security_group.host.id
  description       = "Direct SSH. SSM Session Manager works without this rule."
}

# IAM: SSM-managed instance + container registry pulls.
resource "aws_iam_role" "host" {
  name = "${var.name}-host"

  assume_role_policy = jsonencode({
    Version = "2012-10-17",
    Statement = [{
      Effect    = "Allow",
      Principal = { Service = "ec2.amazonaws.com" },
      Action    = "sts:AssumeRole",
    }],
  })

  tags = local.common_tags
}

resource "aws_iam_role_policy_attachment" "ssm" {
  role       = aws_iam_role.host.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "host" {
  name = "${var.name}-host"
  role = aws_iam_role.host.name
}

# User data: install Docker, drop the compose stack, start systemd unit.
locals {
  user_data = templatefile("${path.module}/user_data.sh.tftpl", {
    image_owner = var.image_owner
    image_tag   = var.image_tag
  })
}

resource "aws_instance" "host" {
  ami                    = data.aws_ami.al2023.id
  instance_type          = var.instance_type
  subnet_id              = data.aws_subnets.default.ids[0]
  vpc_security_group_ids = [aws_security_group.host.id]
  iam_instance_profile   = aws_iam_instance_profile.host.name
  key_name               = var.key_pair_name == "" ? null : var.key_pair_name
  user_data              = local.user_data

  root_block_device {
    volume_size           = var.root_volume_size_gb
    volume_type           = "gp3"
    encrypted             = true
    delete_on_termination = false
  }

  metadata_options {
    http_tokens                 = "required"
    http_put_response_hop_limit = 2
    http_endpoint               = "enabled"
  }

  tags = local.common_tags
}
