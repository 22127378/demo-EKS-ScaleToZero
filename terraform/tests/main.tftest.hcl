# Offline tests: no AWS credentials needed because the provider is mocked.
# Run with: terraform test

mock_provider "helm" {}

mock_provider "aws" {
  mock_data "aws_availability_zones" {
    defaults = {
      names = ["ap-southeast-1a", "ap-southeast-1b", "ap-southeast-1c"]
    }
  }

  mock_data "aws_partition" {
    defaults = {
      id         = "aws"
      partition  = "aws"
      dns_suffix = "amazonaws.com"
    }
  }

  mock_data "aws_caller_identity" {
    defaults = {
      account_id = "123456789012"
      arn        = "arn:aws:iam::123456789012:user/test"
      user_id    = "AIDATESTTESTTESTTEST"
    }
  }

  mock_data "aws_iam_session_context" {
    defaults = {
      issuer_arn = "arn:aws:iam::123456789012:user/test"
    }
  }

  # The plan reads the managed secret ARN of the cluster, which only exists after apply.
  mock_resource "aws_rds_cluster" {
    defaults = {
      master_user_secret = [{
        kms_key_id    = "alias/aws/secretsmanager"
        secret_arn    = "arn:aws:secretsmanager:ap-southeast-1:123456789012:secret:rds!cluster-test"
        secret_status = "active"
      }]
    }
  }

  mock_data "aws_region" {
    defaults = {
      name = "ap-southeast-1"
    }
  }

  # The IAM provider validates policy JSON even in plan, so the mock must return a valid document.
  mock_data "aws_iam_policy_document" {
    defaults = {
      json = "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Action\":\"sts:AssumeRole\",\"Resource\":\"*\"}]}"
    }
  }
}

run "defaults_plan_cleanly" {
  command = plan

  # Pin the value so a developer's local terraform.tfvars cannot change the result.
  variables {
    create_spot_service_linked_role = false
  }

  assert {
    condition     = length(local.azs) == 3
    error_message = "Expected 3 availability zones by default."
  }

  assert {
    condition     = length(aws_ecr_repository.demo) == 3
    error_message = "Expected one ECR repository per demo image."
  }

  assert {
    condition     = aws_vpc_endpoint.s3.vpc_endpoint_type == "Gateway"
    error_message = "The S3 endpoint must be a gateway endpoint (free)."
  }

  assert {
    condition     = length(aws_iam_service_linked_role.spot) == 0
    error_message = "The Spot service-linked role must not be created unless requested."
  }
}

run "two_azs" {
  command = plan

  variables {
    az_count = 2
  }

  assert {
    condition     = length(local.azs) == 2
    error_message = "az_count = 2 must yield two availability zones."
  }
}

run "spot_service_linked_role_on_request" {
  command = plan

  variables {
    create_spot_service_linked_role = true
  }

  assert {
    condition     = length(aws_iam_service_linked_role.spot) == 1
    error_message = "The Spot service-linked role should be created when requested."
  }
}

run "rejects_bad_az_count" {
  command = plan

  variables {
    az_count = 5
  }

  expect_failures = [var.az_count]
}

run "addons_use_system_nodes_and_expected_charts" {
  command = plan

  assert {
    condition     = helm_release.karpenter.chart == "karpenter" && helm_release.karpenter.namespace == "karpenter"
    error_message = "Karpenter must be installed from its chart into the karpenter namespace."
  }

  assert {
    condition     = helm_release.keda_http_addon.chart == "keda-add-ons-http" && helm_release.keda_http_addon.namespace == "keda"
    error_message = "The KEDA HTTP Add-on must be installed next to KEDA in the keda namespace."
  }

  assert {
    condition     = local.system_node_selector == { "node-role" = "system" }
    error_message = "Add-ons must target the system node group label (node-role=system)."
  }

  assert {
    condition     = helm_release.aws_lb_controller.version != "" && helm_release.keda.version != "" && helm_release.keda_http_addon.version != ""
    error_message = "Chart versions must be pinned."
  }
}

run "cluster_config_depends_on_karpenter_and_has_defaults" {
  command = plan

  variables {
    create_spot_service_linked_role = false
  }

  assert {
    condition     = helm_release.cluster_config.chart == "${path.module}/../charts/cluster-config" || endswith(helm_release.cluster_config.chart, "charts/cluster-config")
    error_message = "cluster-config must be installed from the local chart."
  }

  assert {
    condition     = var.default_pool_cpu_limit + var.worker_pool_cpu_limit <= 32
    error_message = "NodePool CPU limits together must stay within the Spot vCPU quota (32)."
  }
}

run "database_never_pauses_and_is_private" {
  command = plan

  variables {
    create_spot_service_linked_role = false
  }

  assert {
    condition     = one(aws_rds_cluster.aurora.serverlessv2_scaling_configuration).min_capacity >= 0.5
    error_message = "Aurora must keep at least 0.5 ACU so it never auto-pauses."
  }

  assert {
    condition     = aws_rds_cluster.aurora.storage_encrypted && aws_rds_cluster.aurora.manage_master_user_password
    error_message = "Storage must be encrypted and the password managed by AWS (never in Terraform values)."
  }

  assert {
    condition     = aws_vpc_security_group_ingress_rule.aurora_from_nodes.from_port == 5432 && aws_vpc_security_group_ingress_rule.aurora_from_nodes.to_port == 5432
    error_message = "The database may only accept PostgreSQL traffic (5432) from the nodes."
  }

  assert {
    condition     = aws_rds_cluster_instance.writer.publicly_accessible == false
    error_message = "The database instance must not be public."
  }
}

run "queue_has_dead_letter_queue" {
  command = plan

  # The DLQ ARN only exists after apply; the redrive policy embeds it, so give it a known value.
  override_resource {
    target          = aws_sqs_queue.jobs_dlq
    override_during = plan
    values = {
      arn = "arn:aws:sqs:ap-southeast-1:123456789012:demo-jobs-dlq"
    }
  }

  variables {
    create_spot_service_linked_role = false
  }

  assert {
    condition     = jsondecode(aws_sqs_queue.jobs.redrive_policy).maxReceiveCount == 3
    error_message = "Failed jobs must move to the dead-letter queue after 3 receives."
  }

  assert {
    condition     = aws_sqs_queue.jobs.receive_wait_time_seconds == 20 && aws_sqs_queue.jobs.sqs_managed_sse_enabled
    error_message = "The queue must use long polling and server-side encryption."
  }
}

run "rejects_db_min_acu_that_allows_pausing" {
  command = plan

  variables {
    db_min_acu = 0
  }

  expect_failures = [var.db_min_acu]
}

run "rejects_db_max_below_min" {
  command = plan

  variables {
    db_min_acu = 1
    db_max_acu = 0.5
  }

  expect_failures = [var.db_max_acu]
}
