package acme

import (
	"context"
	"errors"
	"fmt"
	"strings"

	dnspod "github.com/go-acme/tencentclouddnspod/v20210323"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	sdkerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// tencentCAA: DNSPod takes the record as SubDomain + "0 issue \"...\"" on the default line.
func tencentCAA(ctx context.Context, host, value string, apply bool) (bool, error) {
	id, secret := credEnv("TENCENTCLOUD_SECRET_ID"), credEnv("TENCENTCLOUD_SECRET_KEY")
	if id == "" || secret == "" {
		return false, errors.New("没找到腾讯云的 SecretId / SecretKey")
	}
	cred := common.NewCredential(id, secret)
	if t := credEnv("TENCENTCLOUD_SESSION_TOKEN"); t != "" {
		cred = common.NewTokenCredential(id, secret, t)
	}
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "dnspod.tencentcloudapi.com"
	c, err := dnspod.NewClient(cred, credEnv("TENCENTCLOUD_REGION"), cpf)
	if err != nil {
		return false, fmt.Errorf("腾讯云：%w", err)
	}

	// Match on the punycode form (host is ASCII), call the API with the name DNSPod knows the domain by.
	names := map[string]string{}
	var zones []string
	list := dnspod.NewDescribeDomainListRequest()
	for {
		r, err := dnspod.DescribeDomainListWithContext(ctx, c, list)
		if err != nil {
			return false, fmt.Errorf("腾讯云列域名失败：%w", err)
		}
		for _, d := range r.Response.DomainList {
			p := strings.ToLower(*d.Punycode)
			names[p] = *d.Name
			zones = append(zones, p)
		}
		if len(r.Response.DomainList) == 0 || uint64(len(zones)) >= *r.Response.DomainCountInfo.AllTotal {
			break
		}
		list.Offset = common.Int64Ptr(int64(len(zones)))
	}
	zone, rr, err := pickZone(host, zones)
	if err != nil {
		return false, err
	}

	q := dnspod.NewDescribeRecordListRequest()
	q.Domain, q.Subdomain, q.RecordType = common.StringPtr(names[zone]), common.StringPtr(rr), common.StringPtr("CAA")
	r, err := dnspod.DescribeRecordListWithContext(ctx, c, q)
	var se *sdkerr.TencentCloudSDKError
	switch {
	case errors.As(err, &se) && se.Code == dnspod.RESOURCENOTFOUND_NODATAOFRECORD:
	case err != nil:
		return false, fmt.Errorf("腾讯云读记录失败：%w", err)
	default:
		for _, rec := range r.Response.RecordList {
			if sameCAA(*rec.Value, value) {
				return false, nil
			}
		}
	}
	if !apply {
		return true, nil
	}
	add := dnspod.NewCreateRecordRequest()
	add.Domain, add.SubDomain, add.RecordType = common.StringPtr(names[zone]), common.StringPtr(rr), common.StringPtr("CAA")
	add.RecordLine, add.Value = common.StringPtr("默认"), common.StringPtr(caaRecord(value))
	if _, err := dnspod.CreateRecordWithContext(ctx, c, add); err != nil {
		return false, fmt.Errorf("腾讯云加记录失败：%w", err)
	}
	return true, nil
}
