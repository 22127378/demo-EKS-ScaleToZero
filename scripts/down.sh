#!/usr/bin/env bash
# Tear the whole demo down in an order that leaves nothing behind, so billing stops.
#
# Usage: scripts/down.sh [--dry-run] [--yes]
#   --dry-run  only list what exists; delete nothing
#   --yes      do not ask for confirmation
#
# Order matters:
#   1. uninstall the apps: deleting their Ingress makes the load balancer controller remove the ALB
#   2. delete the placeholder pods and the Karpenter NodePools, wait until Karpenter has terminated the
#      EC2 nodes it launched (they are not in the Terraform state), then remove the node class
#      (Terraform removes Karpenter's IAM permissions, so this must finish before step 3)
#   3. terraform destroy: Aurora, queues, EKS, NAT gateway, VPC, ECR, IAM
#   4. list anything that is still around
set -uo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/_common.sh"

dry=0; yes=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) dry=1 ;;
    --yes) yes=1 ;;
    *) echo "ERROR: unknown option '$arg'" >&2; exit 2 ;;
  esac
done

aws sts get-caller-identity >/dev/null 2>&1 || { echo "ERROR: not logged in to AWS (run: aws login)" >&2; exit 2; }

list_resources() {
  echo "EKS cluster:   $(aws eks describe-cluster --region "$region" --name "$cluster" --query 'cluster.status' --output text 2>/dev/null || echo none)"
  echo "Load balancers: $(aws elbv2 describe-load-balancers --region "$region" --query "LoadBalancers[?starts_with(LoadBalancerName,'k8s-')].LoadBalancerName" --output text 2>/dev/null | wc -w)"
  echo "Aurora:        $(aws rds describe-db-clusters --region "$region" --query "DBClusters[?starts_with(DBClusterIdentifier,'$cluster-')].DBClusterIdentifier" --output text 2>/dev/null | wc -w) cluster(s)"
  echo "EC2 nodes:     $(aws ec2 describe-instances --region "$region" --filters "Name=tag-key,Values=kubernetes.io/cluster/$cluster" "Name=instance-state-name,Values=pending,running,stopping" --query 'Reservations[].Instances[].InstanceId' --output text 2>/dev/null | wc -w)"
  echo "NAT gateways:  $(aws ec2 describe-nat-gateways --region "$region" --filter "Name=tag:Cluster,Values=$cluster" "Name=state,Values=pending,available" --query 'NatGateways[].NatGatewayId' --output text 2>/dev/null | wc -w)"
  echo "SQS queues:    $(aws sqs list-queues --region "$region" --queue-name-prefix "$cluster-" --query 'QueueUrls || `[]`' --output text 2>/dev/null | wc -w)"
  echo "ECR images:    $(for r in demo-api demo-worker demo-web; do aws ecr list-images --region "$region" --repository-name "$r" --query 'length(imageIds)' --output text 2>/dev/null || echo 0; done | awk '{s+=$1} END{print s+0}')"
}

say "Currently existing (cluster '$cluster', region $region)"
list_resources
if (( dry )); then
  echo
  echo "Dry run: nothing was deleted. This is what down.sh would do:"
  echo "  1. helm uninstall fe, api, worker (in namespace demo) and wait for the ALB to disappear"
  echo "  2. delete placeholders and NodePools, wait for the Karpenter nodes to terminate, delete the node class"
  echo "  3. terraform destroy (state: $root/terraform/terraform.tfstate)"
  echo "  4. list what is left"
  exit 0
fi

if (( yes == 0 )); then
  echo
  echo "This DELETES the whole demo, including the Aurora database and its data."
  read -r -p "Type the cluster name '$cluster' to continue: " answer
  [[ "$answer" == "$cluster" ]] || { echo "Aborted."; exit 1; }
fi

if aws eks describe-cluster --region "$region" --name "$cluster" >/dev/null 2>&1; then
  use_cluster
  say "1/4 Remove the applications (and with them the ALB)"
  "$root/scripts/deploy-apps.sh" uninstall || true
  for _ in $(seq 1 40); do
    n="$(aws elbv2 describe-load-balancers --region "$region" --query "LoadBalancers[?starts_with(LoadBalancerName,'k8s-')].LoadBalancerName" --output text 2>/dev/null | wc -w)"
    (( n == 0 )) && break
    echo "  waiting for $n load balancer(s) to be removed..."; sleep 15
  done

  say "2/4 Remove the Karpenter nodes"
  # The placeholder pods carry do-not-disrupt, which would keep their nodes alive: remove them first.
  kubectl -n kube-system delete deploy placeholder-amd64 placeholder-arm64 placeholder-worker --ignore-not-found --wait=false || true
  kubectl delete nodepool --all --wait=false || true
  # Wait until Karpenter has really terminated its EC2 instances. Terraform removes Karpenter's IAM
  # permissions during destroy, so any node left behind here could no longer be cleaned up.
  for _ in $(seq 1 60); do
    claims="$(kubectl get nodeclaim --no-headers 2>/dev/null | wc -l)"
    ec2="$(aws ec2 describe-instances --region "$region" --filters "Name=tag-key,Values=karpenter.sh/nodepool" "Name=tag:eks:eks-cluster-name,Values=$cluster" "Name=instance-state-name,Values=pending,running,shutting-down" --query 'Reservations[].Instances[].InstanceId' --output text 2>/dev/null | wc -w)"
    (( claims == 0 && ec2 == 0 )) && break
    echo "  waiting for Karpenter: $claims nodeclaim(s), $ec2 instance(s) left..."; sleep 15
  done
  if (( claims != 0 || ec2 != 0 )); then
    echo "WARNING: Karpenter nodes are still around ($claims nodeclaim(s), $ec2 instance(s))." >&2
    echo "         Terminate them yourself (aws ec2 terminate-instances) if terraform destroy stalls." >&2
  fi
  # Let Karpenter (still allowed to call AWS) clean up the node class and its instance profile; if it
  # does not finish in time, drop the finalizer so the Helm release can be uninstalled.
  kubectl delete ec2nodeclass --all --timeout=120s || true
  for nc in $(kubectl get ec2nodeclass -o name 2>/dev/null); do
    kubectl patch "$nc" --type=merge -p '{"metadata":{"finalizers":null}}' || true
  done
else
  echo "No EKS cluster named '$cluster': skipping steps 1 and 2."
fi

say "3/4 terraform destroy"
tf init -input=false >/dev/null
tf destroy -input=false -auto-approve || { echo "ERROR: terraform destroy failed; fix the error and run this script again." >&2; exit 1; }

# Karpenter creates one instance profile per node class (named <cluster>_<digits>) that Terraform does not know about.
for ip in $(aws iam list-instance-profiles --query "InstanceProfiles[?starts_with(InstanceProfileName,'${cluster}_')].InstanceProfileName" --output text 2>/dev/null); do
  [[ "$ip" =~ ^${cluster}_[0-9]+$ ]] || continue
  aws iam delete-instance-profile --instance-profile-name "$ip" && echo "Deleted leftover instance profile $ip"
done

say "4/4 What is left"
list_resources
echo
echo "Everything should read 'none' or 0. The ECR images count must be 0 too. Check the AWS console for"
echo "anything unexpected (EC2, load balancers, NAT gateways, RDS, Elastic IPs) before you stop worrying."
