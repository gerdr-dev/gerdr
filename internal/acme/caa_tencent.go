package acme

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gerdr-dev/gerdr/internal/i18n"
	dnspod "github.com/go-acme/tencentclouddnspod/v20210323"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	sdkerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// tencentRecord: DNSPod takes the record as SubDomain + its text ("0 issue \"...\"" for CAA) on the default line.
func tencentRecord(ctx context.Context, host string, want rec, apply bool) (bool, error) {
	id, secret := credEnv("TENCENTCLOUD_SECRET_ID"), credEnv("TENCENTCLOUD_SECRET_KEY")
	if id == "" || secret == "" {
		return false, errors.New(i18n.S("没找到腾讯云的 SecretId / SecretKey", "no Tencent Cloud SecretId / SecretKey found"))
	}
	cred := common.NewCredential(id, secret)
	if t := credEnv("TENCENTCLOUD_SESSION_TOKEN"); t != "" {
		cred = common.NewTokenCredential(id, secret, t)
	}
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "dnspod.tencentcloudapi.com"
	c, err := dnspod.NewClient(cred, credEnv("TENCENTCLOUD_REGION"), cpf)
	if err != nil {
		return false, fmt.Errorf(i18n.S("腾讯云：%w", "Tencent Cloud: %w"), err)
	}

	// Match on the punycode form (host is ASCII), call the API with the name DNSPod knows the domain by.
	names := map[string]string{}
	var zones []string
	list := dnspod.NewDescribeDomainListRequest()
	for {
		r, err := dnspod.DescribeDomainListWithContext(ctx, c, list)
		if err != nil {
			return false, fmt.Errorf(i18n.S("腾讯云列域名失败：%w", "Tencent Cloud: listing domains: %w"), err)
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
	q.Domain, q.Subdomain, q.RecordType = common.StringPtr(names[zone]), common.StringPtr(rr), common.StringPtr(want.Type)
	r, err := dnspod.DescribeRecordListWithContext(ctx, c, q)
	var se *sdkerr.TencentCloudSDKError
	var have []held
	switch {
	case errors.As(err, &se) && se.Code == dnspod.RESOURCENOTFOUND_NODATAOFRECORD:
	case err != nil:
		return false, fmt.Errorf(i18n.S("腾讯云读记录失败：%w", "Tencent Cloud: reading records: %w"), err)
	default:
		for _, rec := range r.Response.RecordList {
			if rec.RecordId != nil && rec.Value != nil {
				have = append(have, held{strconv.FormatUint(*rec.RecordId, 10), *rec.Value})
			}
		}
	}
	ok, others := want.judge(have)
	if ok && len(others) == 0 {
		return false, nil
	}
	if !apply {
		return true, nil
	}
	if !ok {
		add := dnspod.NewCreateRecordRequest()
		add.Domain, add.SubDomain, add.RecordType = common.StringPtr(names[zone]), common.StringPtr(rr), common.StringPtr(want.Type)
		add.RecordLine, add.Value = common.StringPtr("默认"), common.StringPtr(want.text())
		if _, err := dnspod.CreateRecordWithContext(ctx, c, add); err != nil {
			return false, fmt.Errorf(i18n.S("腾讯云加记录失败：%w", "Tencent Cloud: adding the record: %w"), err)
		}
	}
	for _, id := range others {
		del := dnspod.NewDeleteRecordRequest()
		n, _ := strconv.ParseUint(id, 10, 64)
		del.Domain, del.RecordId = common.StringPtr(names[zone]), common.Uint64Ptr(n)
		if _, err := dnspod.DeleteRecordWithContext(ctx, c, del); err != nil {
			return false, fmt.Errorf(i18n.S("腾讯云删旧记录失败：%w", "Tencent Cloud: removing an old record: %w"), err)
		}
	}
	return true, nil
}
