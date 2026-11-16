package citizenid

import (
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/store"
)

func TestBrandFallsBackPerApp(t *testing.T) {
	g, st := newGateway(t)
	saved, err := g.SetBrand(store.SignInBrand{
		Title: "Home", Welcome: "Welcome home.", Accent: "#112233",
		Footer: "Run by the house.",
		Links:  []store.BrandLink{{Label: "Status", URL: "/apps"}},
	})
	if err != nil || saved.Title != "Home" || saved.Accent != "#112233" {
		t.Fatalf("defaults %+v %v", saved, err)
	}
	if _, err := g.SetBrand(store.SignInBrand{Accent: "blue"}); err == nil {
		t.Fatal("accepted a non-hex accent")
	}
	if err := st.Update(func(s *store.State) error {
		s.Publish.Apps = []store.PublishedApp{{
			Container: "files", Hostname: "files.home.arpa", Enabled: true,
			Brand: store.SignInBrand{Title: "Files", Accent: "#aa00aa"},
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	home := g.BrandForHost("feldcore.home.arpa")
	if home.Title != "Home" || home.Welcome != "Welcome home." || home.Accent != "#112233" || home.Background != "#f4f7fd" {
		t.Fatalf("defaults page %+v", home)
	}
	if len(home.Links) != 1 || home.Links[0].URL != "/apps" {
		t.Fatalf("links %+v", home.Links)
	}
	files := g.BrandForHost("files.home.arpa")
	if files.Title != "Files" || files.Accent != "#aa00aa" || files.Welcome != "Welcome home." || files.Footer != "Run by the house." {
		t.Fatalf("app page did not fall back %+v", files)
	}
	dir := g.Directory()
	if dir.Brand.Title != "Home" || dir.Brand.Welcome != "Welcome home." {
		t.Fatalf("directory brand %+v", dir.Brand)
	}
	if strings.Contains(dir.Brand.Background, "f4f7fd") {
		t.Fatal("directory returned a filled default instead of the stored brand")
	}
}
