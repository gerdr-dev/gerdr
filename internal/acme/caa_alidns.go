package acme

import (
	"context"
	"errors"
	"fmt"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	alidns "github.com/go-acme/alidns-20150109/v4/client"
)

// alidnsCAA: AliDNS takes the record as RR (relative name) + "0 issue \"...\"".
func alidnsCAA(ctx context.Context, host, value string, apply bool) (bool, error) {
	id, secret := credEnv("ALICLOUD_ACCESS_KEY"), credEnv("ALICLOUD_SECRET_KEY")
	if id == "" || secret == "" {
		return false, errors.New("没找到阿里云的 AccessKey")
	}
	region := credEnv("ALICLOUD_REGION_ID")
	if region == "" {
		region = "cn-hangzhou"
	}
	cfg := new(openapi.Config).SetRegionId(region).SetAccessKeyId(id).SetAccessKeySecret(secret)
	if t := credEnv("ALICLOUD_SECURITY_TOKEN"); t != "" {
		cfg = cfg.SetSecurityToken(t)
	}
	c, err := alidns.NewClient(cfg)
	if err != nil {
		return false, fmt.Errorf("阿里云：%w", err)
	}
	rt := &dara.RuntimeOptions{}

	var zones []string
	for page := int64(1); ; page++ {
		r, err := alidns.DescribeDomainsWithContext(ctx, c, new(alidns.DescribeDomainsRequest).SetPageNumber(page).SetPageSize(100), rt)
		if err != nil {
			return false, fmt.Errorf("阿里云列域名失败：%w", err)
		}
		for _, d := range r.Body.Domains.Domain {
			zones = append(zones, dara.StringValue(d.DomainName))
		}
		if page*100 >= dara.Int64Value(r.Body.TotalCount) {
			break
		}
	}
	zone, rr, err := pickZone(host, zones)
	if err != nil {
		return false, err
	}

	req := new(alidns.DescribeDomainRecordsRequest).SetDomainName(zone).SetRRKeyWord(rr).SetType("CAA").SetPageSize(500)
	r, err := alidns.DescribeDomainRecordsWithContext(ctx, c, req, rt)
	if err != nil {
		return false, fmt.Errorf("阿里云读记录失败：%w", err)
	}
	for _, rec := range r.Body.DomainRecords.Record {
		// RRKeyWord is a fuzzy match: "gerdr" also returns "gerdr2"
		if dara.StringValue(rec.RR) == rr && sameCAA(dara.StringValue(rec.Value), value) {
			return false, nil
		}
	}
	if !apply {
		return true, nil
	}
	add := new(alidns.AddDomainRecordRequest).SetDomainName(zone).SetRR(rr).SetType("CAA").SetValue(caaRecord(value))
	if _, err := alidns.AddDomainRecordWithContext(ctx, c, add, rt); err != nil {
		return false, fmt.Errorf("阿里云加记录失败：%w", err)
	}
	return true, nil
}
