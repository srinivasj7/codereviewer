output "instance_id" {
  description = "EC2 instance ID; use with SSM Session Manager."
  value       = aws_instance.host.id
}

output "public_dns" {
  description = "Public DNS name."
  value       = aws_instance.host.public_dns
}

output "public_ip" {
  description = "Public IPv4 address."
  value       = aws_instance.host.public_ip
}

output "ssm_session_command" {
  description = "Convenience: aws CLI command to open an SSM shell."
  value       = "aws ssm start-session --region ${var.region} --target ${aws_instance.host.id}"
}
