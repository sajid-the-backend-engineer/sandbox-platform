# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# The db_* outputs use splat + one() rather than a [0] index so that they stay
# evaluable when create_rds is false. An index into a zero-count resource is an
# error even inside the untaken branch of a conditional, and these outputs are
# read by callers that branch on the same flag.
#
# db_name and db_username fall back to the module's own variables rather than to
# empty strings: a caller running Postgres in-cluster still needs to know which
# database and role the api expects, and those are the same values either way.

output "db_host" {
  description = "Postgres endpoint hostname, without the port. Injected as DB_HOST. Empty when create_rds is false."
  value       = one(aws_db_instance.this[*].address) != null ? one(aws_db_instance.this[*].address) : ""
}

output "db_port" {
  description = "Postgres port. Injected as DB_PORT."
  value       = one(aws_db_instance.this[*].port) != null ? one(aws_db_instance.this[*].port) : 5432
}

output "db_name" {
  description = "Application database name. Injected as DB_DATABASE. Falls back to var.db_name when create_rds is false."
  value       = one(aws_db_instance.this[*].db_name) != null ? one(aws_db_instance.this[*].db_name) : var.db_name
}

output "db_username" {
  description = "Postgres master username. Injected as DB_USERNAME. Falls back to var.db_username when create_rds is false."
  value       = one(aws_db_instance.this[*].username) != null ? one(aws_db_instance.this[*].username) : var.db_username
}

output "db_instance_arn" {
  description = "ARN of the RDS instance. Empty when create_rds is false."
  value       = one(aws_db_instance.this[*].arn) != null ? one(aws_db_instance.this[*].arn) : ""
}

output "db_security_group_id" {
  description = "Security group guarding Postgres. Null when create_rds is false -- there is no RDS instance to guard."
  value       = one(aws_security_group.rds[*].id)
}

output "rds_enabled" {
  description = "Whether this module created a managed RDS instance. Mirrors var.create_rds and is safe to branch on at plan time."
  value       = var.create_rds
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
