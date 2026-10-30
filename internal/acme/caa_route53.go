package acme

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53 "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// route53CAA: Route 53 keeps all CAA values of a name in one record set, so "add" is read the set, append, UPSERT.
func route53CAA(ctx context.Context, host, value string, apply bool) (bool, error) {
	var opts []func(*awsconfig.LoadOptions) error
	if id, secret := credEnv("AWS_ACCESS_KEY_ID"), credEnv("AWS_SECRET_ACCESS_KEY"); id != "" && secret != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(id, secret, credEnv("AWS_SESSION_TOKEN"))))
	}
	// Route 53 is global; the SDK still wants a region to sign with.
	region := credEnv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}
	opts = append(opts, awsconfig.WithRegion(region))
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return false, fmt.Errorf("AWS：%w", err)
	}
	c := route53.NewFromConfig(cfg)

	zoneID := credEnv("AWS_HOSTED_ZONE_ID")
	labels := strings.Split(host, ".")
	for i := 0; i < len(labels)-1 && zoneID == ""; i++ {
		name := strings.Join(labels[i:], ".")
		r, err := c.ListHostedZonesByName(ctx, &route53.ListHostedZonesByNameInput{DNSName: aws.String(name)})
		if err != nil {
			return false, fmt.Errorf("Route 53 列 hosted zone 失败：%w", err)
		}
		for _, z := range r.HostedZones {
			if aws.ToString(z.Name) == name+"." && (z.Config == nil || !z.Config.PrivateZone) {
				zoneID = strings.TrimPrefix(aws.ToString(z.Id), "/hostedzone/")
				break
			}
		}
	}
	if zoneID == "" {
		return false, fmt.Errorf("这份凭据下找不到 %s 所在的 hosted zone", host)
	}

	set := r53.ResourceRecordSet{Name: aws.String(host + "."), Type: r53.RRTypeCaa, TTL: aws.Int64(300)}
	r, err := c.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID), StartRecordName: aws.String(host), StartRecordType: r53.RRTypeCaa, MaxItems: aws.Int32(1),
	})
	if err != nil {
		return false, fmt.Errorf("Route 53 读记录失败（IAM 要有 route53:ListResourceRecordSets）：%w", err)
	}
	if len(r.ResourceRecordSets) > 0 && aws.ToString(r.ResourceRecordSets[0].Name) == host+"." && r.ResourceRecordSets[0].Type == r53.RRTypeCaa {
		set = r.ResourceRecordSets[0]
		for _, rec := range set.ResourceRecords {
			if sameCAA(aws.ToString(rec.Value), value) {
				return false, nil
			}
		}
	}
	if !apply {
		return true, nil
	}
	set.ResourceRecords = append(set.ResourceRecords, r53.ResourceRecord{Value: aws.String(caaRecord(value))})
	_, err = c.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53.ChangeBatch{
			Comment: aws.String("gerdr: pin certificate issuance to this machine's ACME account"),
			Changes: []r53.Change{{Action: r53.ChangeActionUpsert, ResourceRecordSet: &set}},
		},
	})
	if err != nil {
		return false, fmt.Errorf("Route 53 写记录失败：%w", err)
	}
	return true, nil
}
