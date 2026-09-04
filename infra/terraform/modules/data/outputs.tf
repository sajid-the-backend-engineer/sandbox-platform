# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

output "db_host" {
  description = "Postgres endpoint hostname, without the port. Injected as DB_HOST."
  value       = aws_db_instance.this.address
}

output "db_port" {
  description = "Postgres port. Injected as DB_PORT."
  value       = aws_db_instance.this.port
}

output "db_name" {
  description = "Application database name. Injected as DB_DATABASE."
  value       = aws_db_instance.this.db_name
}

output "db_username" {
  description = "Postgres master username. Injected as DB_USERNAME."
  value       = aws_db_instance.this.username
}

output "db_instance_arn" {
  description = "ARN of the RDS instance."
  value       = aws_db_instance.this.arn
}

output "db_security_group_id" {
  description = "Security group guarding Postgres."
  value       = aws_security_group.rds.id
}

output "redis_host" {
  description = "Redis primary endpoint. Injected as REDIS_HOST. Clients must connect with TLS -- transit encryption is enforced."
  value       = aws_elasticache_replication_group.this.primary_endpoint_address
}

output "redis_reader_host" {
  description = "Redis reader endpoint, load balanced across replicas. Not currently used by any service but available for read-heavy workloads."
  value       = aws_elasticache_replication_group.this.reader_endpoint_address
}

output "redis_port" {
  description = "Redis port. Injected as REDIS_PORT."
  value       = aws_elasticache_replication_group.this.port
}

output "redis_security_group_id" {
  description = "Security group guarding Redis."
  value       = aws_security_group.redis.id
}

output "artifact_bucket_name" {
  description = "Bucket for user artifact and volume storage. Injected into the api as S3_DEFAULT_BUCKET."
  value       = aws_s3_bucket.this["artifacts"].bucket
}

output "artifact_bucket_arn" {
  description = "ARN of the artifact bucket, for IAM policy scoping."
  value       = aws_s3_bucket.this["artifacts"].arn
}

output "backup_bucket_name" {
  description = "Bucket for runner snapshot backups. Injected into the runner as AWS_DEFAULT_BUCKET."
  value       = aws_s3_bucket.this["backups"].bucket
}

output "backup_bucket_arn" {
  description = "ARN of the backup bucket, for IAM policy scoping."
  value       = aws_s3_bucket.this["backups"].arn
}

output "s3_endpoint" {
  description = <<-EOT
    Regional S3 endpoint for the api's S3_ENDPOINT variable.

    This must NOT contain the substring "minio": the api branches on that substring
    to decide between a MinIO-flavored STS call and a real AWS STS AssumeRole. A
    hostname containing "minio" silently selects the wrong code path.
  EOT
  value       = "https://s3.${data.aws_region.current.name}.amazonaws.com"
}

output "sts_endpoint" {
  description = "Regional STS endpoint for the api's S3_STS_ENDPOINT variable."
  value       = "https://sts.${data.aws_region.current.name}.amazonaws.com"
}
