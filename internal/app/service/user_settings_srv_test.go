package service

import (
	"context"
	"strings"
	"testing"

	"tf-api/internal/app/entities"
	apperrors "tf-api/internal/app/errors"
	"tf-api/internal/app/service/repository"
)

func newSettingsSvc(t *testing.T) *UserSettingsService {
	t.Helper()
	return ProvideUserSettingsService(repository.NewUserSettingsRepository(newTestDB(t)))
}

// TestSettingsRoundTrip 存进去什么、读出来就是什么。
func TestSettingsRoundTrip(t *testing.T) {
	svc := newSettingsSvc(t)
	ctx := context.Background()

	// 没设过 ⇒ nil（不是错误）。客户端据此用默认值。
	got, err := svc.Get(ctx, 1)
	if err != nil {
		t.Fatalf("get empty: %v", err)
	}
	if got != nil {
		t.Fatalf("没设过应该是 nil，拿到 %+v", got)
	}

	payload := `{"childName":"小明","parentPin":"1234","gameDailyMinutes":30,"farmClock":{"speed":2}}`
	saved, err := svc.Save(ctx, 1, payload, 7)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.Payload != payload {
		t.Fatalf("存进去的和读回来的不一样：\n存 %s\n读 %s", payload, saved.Payload)
	}
	if saved.Size != len(payload) {
		t.Fatalf("size = %d，应该是 %d", saved.Size, len(payload))
	}
	if saved.Version != 7 {
		t.Fatalf("version = %d，应该是 7", saved.Version)
	}
	if saved.UpdatedAt == 0 {
		t.Fatal("updated_at 是 0 —— BaseModel 的 BeforeCreate 没跑？")
	}

	// 再读一次（走的是另一条查询路径：Upsert 之后的 Get）
	again, err := svc.Get(ctx, 1)
	if err != nil || again == nil {
		t.Fatalf("re-get: %v %+v", err, again)
	}
	if again.Payload != payload {
		t.Fatalf("二次读回来不一样：%s", again.Payload)
	}
}

// TestSettingsIsolation 一个账号读不到另一个账号的设置。
func TestSettingsIsolation(t *testing.T) {
	svc := newSettingsSvc(t)
	ctx := context.Background()

	if _, err := svc.Save(ctx, 1, `{"childName":"老大"}`, 1); err != nil {
		t.Fatalf("save uid1: %v", err)
	}
	if _, err := svc.Save(ctx, 2, `{"childName":"老二"}`, 1); err != nil {
		t.Fatalf("save uid2: %v", err)
	}

	a, _ := svc.Get(ctx, 1)
	b, _ := svc.Get(ctx, 2)
	if a == nil || b == nil {
		t.Fatal("两个账号都该有设置")
	}
	if !strings.Contains(a.Payload, "老大") || !strings.Contains(b.Payload, "老二") {
		t.Fatalf("串号了：uid1=%s uid2=%s", a.Payload, b.Payload)
	}
}

// TestSettingsOverwrite 第二次保存是**整份覆盖**，不是合并。
//
// ★ 这条钉的是「删除字段」这件事：家长把某个开关从设置里删掉之后，
// 客户端发上来的 JSON 里就没有那个键了。如果服务端做的是「合并」，
// 老键会一直留在库里，下次拉下来又被客户端认回去 —— 开关「复活」。
func TestSettingsOverwrite(t *testing.T) {
	svc := newSettingsSvc(t)
	ctx := context.Background()

	if _, err := svc.Save(ctx, 1, `{"childName":"小明","parentPin":"1234"}`, 1); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	if _, err := svc.Save(ctx, 1, `{"childName":"小明"}`, 2); err != nil {
		t.Fatalf("save 2: %v", err)
	}

	got, err := svc.Get(ctx, 1)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if strings.Contains(got.Payload, "parentPin") {
		t.Fatalf("被删掉的键还在库里（做了合并而不是覆盖）：%s", got.Payload)
	}
	if got.Version != 2 {
		t.Fatalf("version = %d，应该是 2", got.Version)
	}
}

// TestSettingsRejectsBadPayload 形状不对就拒，而且**什么都没写**。
func TestSettingsRejectsBadPayload(t *testing.T) {
	svc := newSettingsSvc(t)
	ctx := context.Background()

	big := `{"x":"` + strings.Repeat("a", entities.MaxUserSettingsBytes) + `"}`

	cases := []struct {
		name    string
		payload string
		want    int
	}{
		{"空串", "", apperrors.SettingsInvalid},
		{"只有空格", "   ", apperrors.SettingsInvalid},
		{"不是 JSON", "childName=小明", apperrors.SettingsInvalid},
		{"顶层是数组", `["小明"]`, apperrors.SettingsInvalid},
		{"顶层是数字", `123`, apperrors.SettingsInvalid},
		{"顶层是字符串", `"小明"`, apperrors.SettingsInvalid},
		{"超过 32 KiB", big, apperrors.SettingsTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.Save(ctx, 9, c.payload, 1)
			if err == nil {
				t.Fatalf("居然存进去了（%d 字节）", len(c.payload))
			}
			if !apperrors.Is(err, c.want) {
				t.Fatalf("错误码不对：%v（期望 %d）", err, c.want)
			}
			// 拒了就不能留痕
			got, _ := svc.Get(ctx, 9)
			if got != nil {
				t.Fatalf("被拒了却写进去了：%+v", got)
			}
		})
	}
}
