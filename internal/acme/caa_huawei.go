package acme

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	hwbasic "github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	hwconfig "github.com/huaweicloud/huaweicloud-sdk-go-v3/core/config"
	hwdns "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dns/v2"
	hwmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dns/v2/model"
	hwregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dns/v2/region"
)

// huaweiCAA: Huawei Cloud DNS keeps all CAA values of a name in one record set, so "add" is read the set,
// append, update (or create the set). The SDK takes no context; the 30s HTTP timeout stands in for it.
//
// Calls go through HcClient.Sync with the generated request defs, as lego does, instead of hwdns.DnsClient:
// that type's hundreds of methods would all be linked in.
func huaweiCAA(_ context.Context, host, value string, apply bool) (bool, error) {
	id, secret, region := credEnv("HUAWEICLOUD_ACCESS_KEY_ID"), credEnv("HUAWEICLOUD_SECRET_ACCESS_KEY"), credEnv("HUAWEICLOUD_REGION")
	if id == "" || secret == "" || region == "" {
		return false, errors.New("没找到华为云的 Access Key / Region")
	}
	auth, err := hwbasic.NewCredentialsBuilder().WithAk(id).WithSk(secret).SafeBuild()
	if err != nil {
		return false, fmt.Errorf("华为云：%w", err)
	}
	reg, err := hwregion.SafeValueOf(region)
	if err != nil {
		return false, fmt.Errorf("华为云：%w", err)
	}
	c, err := hwdns.DnsClientBuilder().
		WithHttpConfig(hwconfig.DefaultHttpConfig().WithTimeout(30 * time.Second)).
		WithRegion(reg).WithCredential(auth).SafeBuild()
	if err != nil {
		return false, fmt.Errorf("华为云：%w", err)
	}

	limit := int32(500)
	zr, err := c.Sync(&hwmodel.ListPublicZonesRequest{Limit: &limit}, hwdns.GenReqDefForListPublicZones())
	if err != nil {
		return false, fmt.Errorf("华为云列域名失败：%w", err)
	}
	ids := map[string]string{}
	var zones []string
	if z := zr.(*hwmodel.ListPublicZonesResponse).Zones; z != nil {
		for _, zone := range *z {
			if zone.Name != nil && zone.Id != nil {
				n := strings.ToLower(strings.TrimSuffix(*zone.Name, "."))
				ids[n] = *zone.Id
				zones = append(zones, n)
			}
		}
	}
	zone, _, err := pickZone(host, zones)
	if err != nil {
		return false, err
	}
	zoneID, fqdn, typ := ids[zone], host+".", "CAA"

	rr, err := c.Sync(&hwmodel.ListRecordSetsByZoneRequest{ZoneId: zoneID, Name: &fqdn, Type: &typ}, hwdns.GenReqDefForListRecordSetsByZone())
	if err != nil {
		return false, fmt.Errorf("华为云读记录失败：%w", err)
	}
	rec := caaRecord(value)
	if sets := rr.(*hwmodel.ListRecordSetsByZoneResponse).Recordsets; sets != nil {
		for _, set := range *sets {
			// the name filter is fuzzy
			if set.Name == nil || *set.Name != fqdn || set.Type == nil || *set.Type != typ || set.Id == nil {
				continue
			}
			var vals []string
			if set.Records != nil {
				vals = *set.Records
			}
			for _, v := range vals {
				if sameCAA(v, value) {
					return false, nil
				}
			}
			if !apply {
				return true, nil
			}
			vals = append(vals, rec)
			_, err := c.Sync(&hwmodel.UpdateRecordSetRequest{ZoneId: zoneID, RecordsetId: *set.Id, Body: &hwmodel.UpdateRecordSetReq{
				Name: set.Name, Description: set.Description, Type: set.Type, Ttl: set.Ttl, Records: &vals,
			}}, hwdns.GenReqDefForUpdateRecordSet())
			if err != nil {
				return false, fmt.Errorf("华为云改记录失败：%w", err)
			}
			return true, nil
		}
	}
	if !apply {
		return true, nil
	}
	ttl := int32(300)
	_, err = c.Sync(&hwmodel.CreateRecordSetRequest{ZoneId: zoneID, Body: &hwmodel.CreateRecordSetRequestBody{
		Name: fqdn, Type: typ, Ttl: &ttl, Records: []string{rec},
	}}, hwdns.GenReqDefForCreateRecordSet())
	if err != nil {
		return false, fmt.Errorf("华为云加记录失败：%w", err)
	}
	return true, nil
}
