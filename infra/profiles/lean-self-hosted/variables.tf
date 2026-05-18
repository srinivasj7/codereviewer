variable "region" {
  description = "AWS region for the single-node deployment."
  type        = string
  default     = "us-east-1"
}

variable "name" {
  description = "Resource name prefix (security group, instance, role)."
  type        = string
  default     = "codereviewer"
}

variable "instance_type" {
  description = "EC2 instance type. Graviton (arm64) variants are recommended for cost."
  type        = string
  default     = "t4g.medium"
}

variable "instance_arch" {
  description = "Instance architecture; controls which AMI gets resolved (arm64|x86_64)."
  type        = string
  default     = "arm64"
  validation {
    condition     = contains(["arm64", "x86_64"], var.instance_arch)
    error_message = "instance_arch must be arm64 or x86_64."
  }
}

variable "root_volume_size_gb" {
  description = "Root EBS volume size; sized for Postgres + embedding_cache + auto-export."
  type        = number
  default     = 50
}

variable "webhook_ingress_cidrs" {
  description = "CIDR blocks allowed to reach the webhook gateway on 443. Default open since GitHub's source IPs rotate; lock down with the documented GitHub IP ranges if your security posture requires it."
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "admin_ingress_cidrs" {
  description = "CIDR blocks allowed to reach the admin UI on 8090. Defaults to no inbound — connect via SSM Session Manager and an SSH tunnel."
  type        = list(string)
  default     = []
}

variable "ssh_ingress_cidrs" {
  description = "CIDR blocks allowed to SSH (22). Empty list disables port 22; SSM Session Manager works regardless."
  type        = list(string)
  default     = []
}

variable "key_pair_name" {
  description = "Optional EC2 key pair for SSH. SSM Session Manager works without one."
  type        = string
  default     = ""
}

variable "image_tag" {
  description = "Container image tag to deploy (e.g. 'v0.5.0' or 'latest')."
  type        = string
  default     = "latest"
}

variable "image_owner" {
  description = "GHCR owner prefix; images resolved as ghcr.io/<owner>-<binary>:<tag>."
  type        = string
}

variable "tags" {
  description = "Additional tags applied to every resource."
  type        = map(string)
  default     = {}
}
