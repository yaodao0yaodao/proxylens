package naming

import (
	"fmt"
	"strings"
)

var countryNames = map[string]string{
	"CN": "中国大陆", "HK": "中国香港", "MO": "中国澳门", "TW": "中国台湾",
	"JP": "日本", "KR": "韩国", "SG": "新加坡", "MY": "马来西亚", "TH": "泰国",
	"VN": "越南", "PH": "菲律宾", "ID": "印度尼西亚", "IN": "印度", "AE": "阿联酋",
	"US": "美国", "CA": "加拿大", "MX": "墨西哥", "BR": "巴西", "AR": "阿根廷",
	"CL": "智利", "GB": "英国", "DE": "德国", "FR": "法国", "NL": "荷兰",
	"IT": "意大利", "ES": "西班牙", "CH": "瑞士", "SE": "瑞典", "FI": "芬兰",
	"NO": "挪威", "DK": "丹麦", "PL": "波兰", "UA": "乌克兰", "RU": "俄罗斯",
	"TR": "土耳其", "IL": "以色列", "AU": "澳大利亚", "NZ": "新西兰", "ZA": "南非",
	"NG": "尼日利亚", "EG": "埃及", "KE": "肯尼亚", "SA": "沙特阿拉伯", "QA": "卡塔尔",
	"PK": "巴基斯坦", "BD": "孟加拉国", "LK": "斯里兰卡", "KZ": "哈萨克斯坦",
	"CZ": "捷克", "AT": "奥地利", "BE": "比利时", "IE": "爱尔兰", "PT": "葡萄牙",
	"RO": "罗马尼亚", "HU": "匈牙利", "GR": "希腊", "IS": "冰岛", "RS": "塞尔维亚",
	"CO": "哥伦比亚", "PE": "秘鲁", "VE": "委内瑞拉", "PR": "波多黎各",
}

func Country(code, fallback string) string {
	if name := countryNames[strings.ToUpper(strings.TrimSpace(code))]; name != "" {
		return name
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		return fallback
	}
	return "未知"
}

// HasCountryHint determines whether a provider label looks like a location
// node rather than account/package information. It intentionally accepts
// common city labels because many mainland providers omit the country name.
func HasCountryHint(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	hints := []string{
		"中国", "中國", "大陆", "大陸", "澳门", "澳門", "香港", "台湾", "台灣",
		"日本", "韩国", "韓國", "新加坡", "美国", "美國", "英国", "英國", "德国", "德國", "法国", "法國",
		"澳大利亚", "澳洲", "加拿大", "印度", "印度尼西亚", "印尼", "马来西亚", "馬來西亞", "菲律宾", "菲律賓",
		"泰国", "泰國", "越南", "俄罗斯", "俄羅斯", "荷兰", "荷蘭", "阿联酋", "阿聯酋", "土耳其", "巴西",
		"阿根廷", "智利", "墨西哥", "瑞士", "瑞典", "芬兰", "芬蘭", "挪威", "丹麦", "丹麥", "波兰", "波蘭",
		"乌克兰", "烏克蘭", "以色列", "南非", "沙特", "卡塔尔", "卡塔爾", "哈萨克斯坦", "哈薩克斯坦",
		"北京", "上海", "广州", "廣州", "深圳", "杭州", "成都", "重庆", "重慶", "武汉", "武漢", "南京", "苏州", "蘇州",
		"青岛", "青島", "厦门", "廈門", "福州", "天津", "西安", "郑州", "鄭州", "长沙", "長沙", "昆明", "沈阳", "瀋陽",
		"大连", "大連", "哈尔滨", "哈爾濱", "济南", "濟南", "合肥", "南宁", "南寧", "海口",
		"hong kong", "macau", "macao", "japan", "tokyo", "osaka", "united states", "usa", "singapore", "taiwan", "korea",
		"germany", "france", "canada", "australia", "india", "malaysia", "thailand", "vietnam", "russia", "netherlands",
		"united kingdom", "london", "turkey", "türkiye", "brazil", "argentina", "chile", "mexico", "switzerland", "sweden",
		"finland", "norway", "denmark", "poland", "ukraine", "israel", "south africa", "uae", "dubai", "saudi", "qatar",
	}
	for _, hint := range hints {
		if strings.Contains(value, hint) {
			return true
		}
	}
	return false
}

func DisplayName(code, fallback string, number int64, multiplier float64) string {
	name := Country(code, fallback)
	if multiplier == 1 {
		return fmt.Sprintf("%s %03d", name, number)
	}
	return fmt.Sprintf("%s %03d %.2gx", name, number, multiplier)
}
