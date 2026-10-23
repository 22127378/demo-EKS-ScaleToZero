# Offline tests: no AWS credentials needed because the provider is mocked.
# Run with: terraform test

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
