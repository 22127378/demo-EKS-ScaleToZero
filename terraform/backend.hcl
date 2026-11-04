bucket       = "demo-tfstate-728384804953"
key          = "demo/terraform.tfstate"
region       = "ap-southeast-1"
encrypt      = true
use_lockfile = true # S3-native locking (Terraform 1.10+), no DynamoDB table needed
