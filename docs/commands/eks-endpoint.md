# kdiag eks endpoint

Check whether core AWS services route over private VPC endpoints or the public internet.

## Synopsis

```
kdiag eks endpoint [flags]
```

## Description

`kdiag eks endpoint` resolves the DNS names of key AWS services and classifies each resolved IP as `"private"` (RFC 1918, loopback, link-local, or IPv6 ULA/loopback) or `"public"`. It also checks the EKS API server endpoint itself.

**Services checked:**

| Service key | DNS name |
|-------------|----------|
| `sts` | `sts.<region>.amazonaws.com` |
| `ec2` | `ec2.<region>.amazonaws.com` |
| `ecr.api` | `api.ecr.<region>.amazonaws.com` |
| `ecr.dkr` | `dkr.ecr.<region>.amazonaws.com` |
| `s3` | `s3.<region>.amazonaws.com` |
| `logs` | `logs.<region>.amazonaws.com` |

**Two-phase check:**

1. **DNS phase** — always runs. Resolves each service hostname from the machine running kdiag and classifies the result. A `private` result indicates a VPC endpoint or split-horizon DNS is routing traffic privately.
2. **EC2 API phase** — runs when AWS credentials are available. Calls `DescribeVpcEndpoints` to enrich each service with its VPC endpoint type (`Interface`/`Gateway`) and current state. When enrichment fails, a warning is printed to stderr and the output falls back to DNS-only results.

The region is auto-detected from the EKS API server hostname when `--region` is omitted.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--profile` | — | AWS shared config profile to use |
| `--region` | auto-detected | AWS region (parsed from the EKS API server endpoint when omitted) |

All [global flags](../README.md#global-flags) also apply (`--output`, `--timeout`, `--verbose`, etc.).

## Examples

**Check which services are routed over the public internet:**

```bash
kdiag eks endpoint
```

**Get JSON output for scripting:**

```bash
kdiag eks endpoint -o json
```

**Use a specific AWS profile:**

```bash
kdiag eks endpoint --profile prod
```

## Output

**Table output — DNS only (no EC2 credentials):**

```
Region:         us-east-1
EKS API Access: private
Note: DNS results only — no EC2 API access

SERVICE    DNS_RESULT
sts        private
ec2        public
ecr.api    private
ecr.dkr    private
s3         public
logs       private
```

**Table output — enriched with EC2 API:**

```
Region:         us-east-1
EKS API Access: private

SERVICE    DNS_RESULT   ENDPOINT_TYPE   ENDPOINT_ID             STATE
sts        private      Interface       vpce-0a1b2c3d4e5f6a7b8   available
ec2        public
ecr.api    private      Interface       vpce-0f1e2d3c4b5a6978    available
ecr.dkr    private      Interface       vpce-09876543210abcdef   available
s3         public
logs       private      Interface       vpce-abcdef1234567890    available
```

A service with `DNS_RESULT: public` and no endpoint entries is routing over the internet. Consider adding a VPC endpoint to avoid internet traffic and potential connectivity issues in locked-down VPCs.

**JSON output (`-o json`):**

```json
{
  "region": "us-east-1",
  "eks_api_access": "private",
  "api_enriched": true,
  "services": [
    {
      "service_key": "sts",
      "dns_result": "private",
      "endpoint_type": "Interface",
      "endpoint_id": "vpce-0a1b2c3d4e5f6a7b8",
      "state": "available"
    },
    {
      "service_key": "ec2",
      "dns_result": "public",
      "endpoint_type": "",
      "endpoint_id": "",
      "state": ""
    }
  ]
}
```

`api_enriched: false` means only DNS results are present — `endpoint_type`, `endpoint_id`, and `state` will be empty strings for all services.

## Required Permissions

**Kubernetes RBAC:** none — only the kubeconfig server URL is used to detect the region.

**IAM (optional, for EC2 enrichment):**

| Action | Purpose |
|--------|---------|
| `ec2:DescribeVpcEndpoints` | Enrich DNS results with VPC endpoint type and state |

Without IAM credentials the command runs in DNS-only mode and still classifies services as `"private"` or `"public"`.

Minimum IAM policy for full enrichment:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["ec2:DescribeVpcEndpoints"],
      "Resource": "*"
    }
  ]
}
```

## Troubleshooting

**"Not an EKS cluster"**

This command requires a kubeconfig pointing at an EKS cluster. The region is derived from the EKS API server hostname (`<id>.<az>.<region>.eks.amazonaws.com`). Local or non-EKS clusters will fail this check.

**`[kdiag] warning: VPC endpoint enrichment failed: ...`**

The EC2 credentials were found but `DescribeVpcEndpoints` returned an error (permission denied, network issue, etc.). The output falls back to DNS-only results. Add `ec2:DescribeVpcEndpoints` to the IAM policy or check network connectivity to the EC2 endpoint.

**Service shows `dns_result: public` but you have a VPC endpoint**

The DNS query runs from the machine running kdiag, not from inside the cluster. If kdiag runs outside the VPC, it will not see VPC-internal DNS resolution. Run with EC2 credentials so the `api_enriched` path can confirm the endpoint exists and is `available` regardless of where DNS resolves.
