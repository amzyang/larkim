package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContact_AccountSuffixIsTheDisambiguatingNumber(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    Contact
		want string
	}{
		{"enterprise email wins", Contact{EnterpriseEmail: "liming01@example.com", Email: "lm@x.cn"}, "01"},
		{"falls back to email", Contact{Email: "wanglei07@example.com"}, "07"},
		{"no suffix means no collision", Contact{EnterpriseEmail: "linlan@example.com"}, ""},
		{"all digits is not a suffix", Contact{EnterpriseEmail: "12345@example.com"}, ""},
		{"no address at all", Contact{}, ""},
		{"bare local part", Contact{EnterpriseEmail: "huyumeng01"}, "01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.c.AccountSuffix())
		})
	}
}

func TestContactsNeedingDetail_PrefersP2PPartners(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.UpsertContacts(ctx, []Contact{
		{OpenID: "ou_group_only", Name: "G"},
		{OpenID: "ou_partner", Name: "P", P2PChatID: "oc_p"},
		{OpenID: "ou_bot", Name: "B", IsBot: true},
	}, 1))

	need, err := s.ContactsNeedingDetail(ctx, 10)
	require.NoError(t, err)
	require.Len(t, need, 2, "bots carry no tenant account")
	require.Equal(t, "ou_partner", need[0].OpenID)
}

func TestSetContactDetails_MarksIDsTheServerOmitted(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.UpsertContacts(ctx, []Contact{
		{OpenID: "ou_found", Name: "stale"},
		{OpenID: "ou_gone", Name: "Left"},
	}, 1))

	require.NoError(t, s.SetContactDetails(ctx,
		[]string{"ou_found", "ou_gone"},
		[]ContactDetail{{OpenID: "ou_found", Name: "李明", EnterpriseEmail: "liming01@example.com", Department: "产品部"}},
		42))

	found, err := s.GetContact(ctx, "ou_found")
	require.NoError(t, err)
	require.Equal(t, "李明", found.Name)
	require.Equal(t, "01", found.AccountSuffix())
	require.Equal(t, "产品部", found.Department)

	gone, err := s.GetContact(ctx, "ou_gone")
	require.NoError(t, err)
	require.Equal(t, "Left", gone.Name, "an unresolved id keeps what it had")
	require.NotZero(t, gone.DetailCheckedAt, "and is not asked for again")

	need, err := s.ContactsNeedingDetail(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, need)
}

func TestUpsertContacts_KeepsResolvedDetails(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.UpsertContacts(ctx, []Contact{{OpenID: "ou_a", Name: "A"}}, 1))
	require.NoError(t, s.SetContactDetails(ctx, []string{"ou_a"},
		[]ContactDetail{{OpenID: "ou_a", Name: "A", EnterpriseEmail: "a01@x.cn", Department: "研发部"}}, 2))

	// A later member listing knows only the open_id and name.
	require.NoError(t, s.UpsertContacts(ctx, []Contact{{OpenID: "ou_a", Name: "A"}}, 3))

	c, err := s.GetContact(ctx, "ou_a")
	require.NoError(t, err)
	require.Equal(t, "a01@x.cn", c.EnterpriseEmail)
	require.Equal(t, "研发部", c.Department)
	require.Equal(t, int64(2), c.DetailCheckedAt)
}

func TestContactsByIDs_SkipsUnknownIDs(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.UpsertContacts(ctx, []Contact{
		{OpenID: "ou_a", Name: "A", Email: "a@x.cn"},
		{OpenID: "cli_bot", Name: "Bot", IsBot: true},
	}, 1))

	got, err := s.ContactsByIDs(ctx, []string{"ou_a", "cli_bot", "ou_missing"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "a@x.cn", got["ou_a"].Email)
	require.True(t, got["cli_bot"].IsBot)
	require.NotContains(t, got, "ou_missing")
}

func TestSetContactDetails_KeepsAnEmailTheLookupOmits(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.UpsertContacts(ctx, []Contact{{OpenID: "ou_a", Name: "A", Email: "a@example.com"}}, 1))

	// The tenant search answered for the id but carried no address.
	require.NoError(t, s.SetContactDetails(ctx, []string{"ou_a"}, []ContactDetail{{OpenID: "ou_a", Name: "A", Department: "Tech"}}, 2))

	got, err := s.GetContact(ctx, "ou_a")
	require.NoError(t, err)
	require.Equal(t, "a@example.com", got.Email, "a resolved address survives a lookup that does not repeat it")
	require.Equal(t, "Tech", got.Department)
	found, err := s.FindContacts(ctx, "a@example.com")
	require.NoError(t, err)
	require.Len(t, found, 1, "and the contact stays findable by it")
}

func TestContactAvatarFile_HoldsBackTheSentinels(t *testing.T) {
	require.Equal(t, "users/ou_a.png", Contact{AvatarPath: "users/ou_a.png"}.AvatarFile())
	require.Empty(t, Contact{AvatarPath: AvatarNone}.AvatarFile(), "a contact known to have no picture")
	require.Empty(t, Contact{AvatarPath: AvatarFailed}.AvatarFile(), "a download that gave up")
	require.Empty(t, Contact{}.AvatarFile())
}
