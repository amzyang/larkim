package cli

import (
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestFuzzyChats_ReachesAChineseNameByItsInitials(t *testing.T) {
	chats := []store.Chat{
		{ChatID: "oc_a", Name: "平台组"},
		{ChatID: "oc_b", Name: "项目协作群"},
	}
	got := fuzzyChats(chats, "ptz", 0)
	require.Len(t, got, 1)
	require.Equal(t, "oc_a", got[0].ChatID)
}

func TestFuzzyChats_KeepsTheStoreOrder(t *testing.T) {
	chats := []store.Chat{
		{ChatID: "oc_a", Name: "抖音自动化测试"},
		{ChatID: "oc_b", Name: "抖音"},
	}
	got := fuzzyChats(chats, "dy", 0)
	require.Equal(t, []string{"oc_a", "oc_b"}, []string{got[0].ChatID, got[1].ChatID})
}

func TestFuzzyChats_AppliesTheLimitAfterMatching(t *testing.T) {
	chats := []store.Chat{
		{ChatID: "oc_a", Name: "财务组"},
		{ChatID: "oc_b", Name: "平台组"},
		{ChatID: "oc_c", Name: "平台二组"},
	}
	got := fuzzyChats(chats, "ptz", 1)
	require.Len(t, got, 1)
	require.Equal(t, "oc_b", got[0].ChatID, "the limit must not eat hits that sit past it")
}

func TestFuzzyChats_EmptySearchTakesEverything(t *testing.T) {
	chats := []store.Chat{{ChatID: "oc_a", Name: "平台组"}}
	require.Len(t, fuzzyChats(chats, "", 0), 1)
}

func TestFuzzyContacts_ReachesANameByPinyinAndAnAddressBySubstring(t *testing.T) {
	people := []store.Contact{
		{OpenID: "ou_a", Name: "张三", Email: "zhangsan@example.com"},
		{OpenID: "ou_b", Name: "李四", Email: "lisi@example.com"},
	}
	require.Len(t, fuzzyContacts(people, "zs", 0), 1)
	require.Equal(t, "ou_b", fuzzyContacts(people, "lisi@", 0)[0].OpenID)
	require.Empty(t, fuzzyContacts(people, "wangwu", 0))
}
