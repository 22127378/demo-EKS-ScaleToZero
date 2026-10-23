resource "aws_ecr_repository" "demo" {
  for_each = toset(var.ecr_repositories)

  name = each.key

  # Demo only: lets `terraform destroy` remove repositories that still hold images.
  force_delete         = true
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = false
  }
}

resource "aws_ecr_lifecycle_policy" "demo" {
  for_each   = aws_ecr_repository.demo
  repository = each.value.name

  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Keep the 5 most recent images"
      selection = {
        tagStatus   = "any"
        countType   = "imageCountMoreThan"
        countNumber = 5
      }
      action = { type = "expire" }
    }]
  })
}
