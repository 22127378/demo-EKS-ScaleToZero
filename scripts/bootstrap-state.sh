#!/usr/bin/env bash
# Create the S3 bucket that holds the Terraform state (terraform/backend.hcl). Safe to run again.
#
# Usage: scripts/bootstrap-state.sh
#
# The bucket is versioned (a bad apply can be rolled back by restoring an older state version), encrypted,
# closed to the public, and kept outside Terraform so `terraform destroy` cannot delete it.
set -euo pipefail
region="${AWS_REGION:-ap-southeast-1}"
account="$(aws sts get-caller-identity --query Account --output text)"
bucket="demo-tfstate-$account"

if aws s3api head-bucket --bucket "$bucket" --region "$region" 2>/dev/null; then
  echo "Bucket $bucket already exists."
else
  aws s3api create-bucket --bucket "$bucket" --region "$region" \
    --create-bucket-configuration "LocationConstraint=$region" >/dev/null
  echo "Created bucket $bucket."
fi

aws s3api put-public-access-block --bucket "$bucket" --region "$region" \
  --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
aws s3api put-bucket-versioning --bucket "$bucket" --region "$region" \
  --versioning-configuration Status=Enabled
aws s3api put-bucket-encryption --bucket "$bucket" --region "$region" \
  --server-side-encryption-configuration '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}'
aws s3api put-bucket-lifecycle-configuration --bucket "$bucket" --region "$region" \
  --lifecycle-configuration '{"Rules":[{"ID":"expire-old-state-versions","Status":"Enabled","Filter":{},"NoncurrentVersionExpiration":{"NoncurrentDays":90},"AbortIncompleteMultipartUpload":{"DaysAfterInitiation":7}}]}'
echo "Done. Next: cd terraform && terraform init -migrate-state -backend-config=backend.hcl"
