# Tells someone when work ends up in the dead-letter queue. Nothing reads that queue on its own, so without an
# alarm a failed screening would sit there until the 14-day retention deletes it.

variable "alarm_email" {
  description = "E-mail address that gets the dead-letter queue alarm. Empty creates the topic but no subscription (AWS sends a confirmation link to the address)."
  type        = string
  default     = ""
}

resource "aws_sns_topic" "alarms" {
  name = "${local.name}-alarms"
}

resource "aws_sns_topic_subscription" "alarm_email" {
  count = var.alarm_email == "" ? 0 : 1

  topic_arn = aws_sns_topic.alarms.arn
  protocol  = "email"
  endpoint  = var.alarm_email
}

resource "aws_cloudwatch_metric_alarm" "jobs_dlq_not_empty" {
  alarm_name        = "${local.name}-jobs-dlq-not-empty"
  alarm_description = "A message moved to the dead-letter queue: a screening failed on every attempt. Check the worker logs; the application is marked 'failed'."

  namespace   = "AWS/SQS"
  metric_name = "ApproximateNumberOfMessagesVisible"
  dimensions  = { QueueName = aws_sqs_queue.jobs_dlq.name }

  statistic           = "Maximum"
  period              = 60
  evaluation_periods  = 1
  threshold           = 0
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"

  alarm_actions = [aws_sns_topic.alarms.arn]
  ok_actions    = [aws_sns_topic.alarms.arn]
}
