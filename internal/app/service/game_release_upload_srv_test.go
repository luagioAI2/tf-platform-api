package service

import "testing"

// 上传接口会拿这两个字段**拼路径**，紧接着 `RemoveAll` 那个目录。
// 所以「哪些值算合法」是安全边界，不是格式偏好 —— 一条都不能松。
func TestSafePathSegment(t *testing.T) {
	ok := []string{
		"demo",
		"cloudfarm",
		"1.0.0",
		"v1.2.3-beta_1",
		"Game_9",
	}
	for _, v := range ok {
		if !safePathSegment(v) {
			t.Errorf("应当放行：%q", v)
		}
	}

	bad := []string{
		"",          // 空 = 拼出来少一层
		".",         // 当前目录
		"..",        // 上一级：路径穿越
		"../../etc", // 经典穿越
		"a/b",       // 分隔符 = 多一层
		`a\b`,       // Windows 分隔符
		"C:windows", // 盘符（Windows 上 Join 会跳盘）
		" demo",     // 空格
		"demo ",     // 尾空格（Windows 会静默裁掉）
		"demo\x00",  // 截断
		"中文",        // 非 ASCII：编码不同就成两个目录
		".hidden",   // 以点开头
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", // 65 字符
	}
	for _, v := range bad {
		if safePathSegment(v) {
			t.Errorf("应当拒绝：%q", v)
		}
	}
}
