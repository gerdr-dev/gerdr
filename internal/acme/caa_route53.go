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
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// route53Record: Route 53 keeps all values of a name and type in one record set, so "add" is read the set, append
// (or, for only() types, replace), UPSERT.
func route53Record(ctx context.Context, host string, want rec, apply bool) (bool, error) {
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
			return false, fmt.Errorf(i18n.S("Route 53 列 hosted zone 失败：%w", "Route 53: listing hosted zones: %w"), err)
		}
		for _, z := range r.HostedZones {
			if aws.ToString(z.Name) == name+"." && (z.Config == nil || !z.Config.PrivateZone) {
				zoneID = strings.TrimPrefix(aws.ToString(z.Id), "/hostedzone/")
				break
			}
		}
	}
	if zoneID == "" {
		return false, fmt.Errorf(i18n.S("这份凭据下找不到 %s 所在的 hosted zone", "no hosted zone for %s under these credentials"), host)
	}

	typ := r53.RRType(want.Type)
	set := r53.ResourceRecordSet{Name: aws.String(host + "."), Type: typ, TTL: aws.Int64(300)}
	r, err := c.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID), StartRecordName: aws.String(host), StartRecordType: typ, MaxItems: aws.Int32(1),
	})
	if err != nil {
		return false, fmt.Errorf(i18n.S("Route 53 读记录失败（IAM 要有 route53:ListResourceRecordSets）：%w", "Route 53: reading records (IAM needs route53:ListResourceRecordSets): %w"), err)
	}
	if len(r.ResourceRecordSets) > 0 && aws.ToString(r.ResourceRecordSets[0].Name) == host+"." && r.ResourceRecordSets[0].Type == typ {
		set = r.ResourceRecordSets[0]
		var have []held
		for _, rec := range set.ResourceRecords {
			have = append(have, held{"", aws.ToString(rec.Value)})
		}
		// An alias A record points at an AWS resource, not at the relay.
		if ok, others := want.judge(have); ok && len(others) == 0 && set.AliasTarget == nil {
			return false, nil
		}
	}
	if !apply {
		return true, nil
	}
	ours := r53.ResourceRecord{Value: aws.String(want.text())}
	if want.only() {
		set.ResourceRecords, set.AliasTarget = []r53.ResourceRecord{ours}, nil
		if set.TTL == nil {
			set.TTL = aws.Int64(300)
		}
	} else {
		set.ResourceRecords = append(set.ResourceRecords, ours)
	}
	_, err = c.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53.ChangeBatch{
			Comment: aws.String("gerdr: remote access (" + want.Type + ")"),
			Changes: []r53.Change{{Action: r53.ChangeActionUpsert, ResourceRecordSet: &set}},
		},
	})
	if err != nil {
		return false, fmt.Errorf(i18n.S("Route 53 写记录失败：%w", "Route 53: writing the record: %w"), err)
	}
	return true, nil
}
