# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

variable "name" {
  description = "Base name for data-tier resources."
  type        = string
}

variable "vpc_id" {
  description = "VPC the data tier lives in."
  type        = string
}

variable "database_subnet_ids" {
  description = "Subnets for the RDS and ElastiCache subnet groups. Must be private with no internet route, and must span at least two AZs."
  type        = list(string)

  validation {
    condition     = length(var.database_subnet_ids) >= 2
    error_message = "At least two subnets in different AZs are required for RDS and ElastiCache subnet groups."
  }
}

variable "allowed_security_group_ids" {
  description = "Security groups permitted to reach Postgres and Redis. In practice: the ECS task security groups and the runner ASG security group."
  type        = list(string)
  default     = []
}

# ---------------------------------------------------------------------------
# Credentials
#
# Passwords are never passed into this module as plaintext. The caller supplies
# the ARN of a Secrets Manager secret and this module reads the current value at
# plan time. That means the secret must hold a real value BEFORE the first apply
# of the data tier -- see the bootstrap order in the README.
# ---------------------------------------------------------------------------

variable "db_password_secret_arn" {
  description = "ARN of the Secrets Manager secret holding the Postgres master password. Read at plan time; never written by this module."
  type        = string
}

variable "redis_auth_token_secret_arn" {
  description = "ARN of the Secrets Manager secret holding the Redis AUTH token. Must be 16-128 printable characters."
  type        = string
}

# ---------------------------------------------------------------------------
# Postgres
# ---------------------------------------------------------------------------

variable "create_rds" {
  description = <<-EOT
    Create the managed RDS Postgres instance and everything that only exists to
    serve it: the subnet group, parameter group, security group and enhanced
    monitoring role.

    False is for callers that run Postgres as a container in the ECS cluster
    instead (see environments/production/postgres.tf). Redis and the S3 buckets
    in this module are unaffected either way.

    This is a plain configuration value on purpose: it drives `count` on several
    resources below, and a count must be decidable at plan time.
  EOT
  type        = bool
  default     = true
}

variable "db_engine_version" {
  description = "Postgres major.minor version. Only the major version is pinned in the parameter group family."
  type        = string
  default     = "16.4"
}

variable "db_instance_class" {
  description = "RDS instance class. db.t4g.medium is a reasonable starting point; move to db.m7g once sandbox creation volume makes the burst credit balance a concern."
  type        = string
  default     = "db.t4g.medium"
}

variable "db_allocated_storage" {
  description = "Initial storage in GiB."
  type        = number
  default     = 50
}

variable "db_max_allocated_storage" {
  description = "Upper bound for RDS storage autoscaling in GiB. Set equal to db_allocated_storage to disable autoscaling."
  type        = number
  default     = 500
}

variable "db_name" {
  description = "Name of the single application database. All three TypeORM migration phases target this one database."
  type        = string
  default     = "northrays"
}

variable "db_username" {
  description = "Postgres master username. 'admin' and 'postgres' are reserved by RDS and will be rejected."
  type        = string
  default     = "northrays"
}

variable "db_multi_az" {
  description = "Run a synchronous standby in a second AZ. Roughly doubles instance cost and is the single biggest availability lever for this stack -- the api is hard-down without Postgres."
  type        = bool
  default     = true
}

variable "db_backup_retention_days" {
  description = "Automated backup retention in days. Zero disables backups entirely and also disables point-in-time recovery."
  type        = number
  default     = 14
}

variable "db_deletion_protection" {
  description = "Refuse to delete the instance. Terraform destroy will fail while this is on, which is the point."
  type        = bool
  default     = true
}

variable "db_skip_final_snapshot" {
  description = "Skip the final snapshot on delete. Leave false in production."
  type        = bool
  default     = false
}

variable "db_performance_insights_enabled" {
  description = "Enable RDS Performance Insights. Free at 7-day retention and the fastest way to find a slow query in production."
  type        = bool
  default     = true
}

# ---------------------------------------------------------------------------
# Redis
# ---------------------------------------------------------------------------

variable "redis_engine_version" {
  description = "ElastiCache Redis engine version."
  type        = string
  default     = "7.1"
}

variable "redis_node_type" {
  description = "ElastiCache node type. Redis here is used for caching, rate limiting and short-TTL dedup, so working set is small; memory pressure is unlikely to be the constraint."
  type        = string
  default     = "cache.t4g.micro"
}

variable "redis_replica_count" {
  description = "Number of read replicas. One replica plus automatic failover gives AZ redundancy; zero means a node failure is a hard outage for cache and rate limiting."
  type        = number
  default     = 1
}

variable "redis_snapshot_retention_days" {
  description = "Daily snapshot retention. Redis holds no source of truth here, so this is a convenience rather than a durability requirement."
  type        = number
  default     = 3
}

# ---------------------------------------------------------------------------
# S3
# ---------------------------------------------------------------------------

variable "bucket_prefix" {
  description = <<-EOT
    Prefix for S3 bucket names. Bucket names are globally unique across all of AWS,
    so the account ID is appended automatically to avoid collisions with other
    tenants who picked the same prefix.
  EOT
  type        = string
  default     = "northrays-production"
}

variable "artifact_bucket_noncurrent_expiry_days" {
  description = "Days before non-current object versions in the artifact bucket are permanently deleted."
  type        = number
  default     = 30
}

variable "backup_bucket_expiry_days" {
  description = "Days before runner snapshot backups are expired. Zero disables expiry and lets the bucket grow without bound."
  type        = number
  default     = 90
}

variable "postgres_dump_prefix" {
  description = "Key prefix in the backup bucket that scheduled pg_dump output is written under. Only meaningful when create_rds is false."
  type        = string
  default     = "postgres/"
}

variable "postgres_dump_retention_days" {
  description = <<-EOT
    Days before scheduled pg_dump objects under postgres_dump_prefix are expired.

    Zero disables the prefix rule, in which case dumps fall under the bucket-wide
    backup_bucket_expiry_days rule instead. Set this SHORTER than
    backup_bucket_expiry_days or it has no effect: when two lifecycle rules match
    one object, the earlier expiry wins.
  EOT
  type        = number
  default     = 0
}

variable "tags" {
  description = "Common tags applied to every resource in this module."
  type        = map(string)
  default     = {}
}
