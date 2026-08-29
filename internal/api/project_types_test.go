package api

import (
	"strings"
	"testing"

	"github.com/hoveychen/muvee/internal/store"
)

func TestNormaliseEnabledProjectTypes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty means no restriction", "", ""},
		{"whitespace only means no restriction", "   ", ""},
		{"single type", "compose", "compose"},
		{"trims and lowercases", " Compose , IMAGE ", "compose,image"},
		{"deduplicates", "image,image,image", "image"},
		// Output order follows store.AllProjectTypes, not input order, so the
		// stored value is stable no matter how the admin UI serialises it.
		{"canonical order", "domain_only,build,deployment", "deployment,build,domain_only"},
		{"drops empty tokens", "compose,,image,", "compose,image"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := normaliseEnabledProjectTypes(c.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("normaliseEnabledProjectTypes(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNormaliseEnabledProjectTypes_UnknownType(t *testing.T) {
	if _, err := normaliseEnabledProjectTypes("compose,bogus"); err == nil {
		t.Fatal("expected an error for an unknown project type")
	} else if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should name the offending type, got %v", err)
	}
}

// A value that is non-empty but names no type must not collapse to "" — that
// would silently flip "allow nothing" into "allow everything".
func TestNormaliseEnabledProjectTypes_RejectsNoTypeAfterCleanup(t *testing.T) {
	if _, err := normaliseEnabledProjectTypes(", ,,"); err == nil {
		t.Fatal("expected an error when the list names no project type")
	}
}

func TestParseEnabledProjectTypes(t *testing.T) {
	all := store.AllProjectTypes
	cases := []struct {
		name string
		in   string
		want []store.ProjectType
	}{
		{"unset allows everything", "", all},
		{"garbage allows everything", "nonsense,???", all},
		{"explicit subset", "compose,image", []store.ProjectType{store.ProjectTypeCompose, store.ProjectTypeImage}},
		{"unknown tokens are ignored around known ones", "compose,bogus", []store.ProjectType{store.ProjectTypeCompose}},
		{"canonical order regardless of input order", "domain_only,deployment",
			[]store.ProjectType{store.ProjectTypeDeployment, store.ProjectTypeDomainOnly}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseEnabledProjectTypes(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("parseEnabledProjectTypes(%q) = %v, want %v", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("parseEnabledProjectTypes(%q) = %v, want %v", c.in, got, c.want)
				}
			}
		})
	}
}

// The builder-backed types are the point of the feature: an operator short on
// memory turns these two off and keeps the image-pulling ones.
func TestParseEnabledProjectTypes_ExcludesBuilderTypes(t *testing.T) {
	got := parseEnabledProjectTypes("compose,image,domain_only")
	for _, t2 := range got {
		if t2 == store.ProjectTypeDeployment || t2 == store.ProjectTypeBuild {
			t.Fatalf("builder-backed type %q should not be allowed, got %v", t2, got)
		}
	}
}

func TestEnabledProjectTypesSettingIsWritable(t *testing.T) {
	if !allowedSettingKey(settingEnabledProjectTypes) {
		t.Fatalf("%s must be in the admin settings write allowlist", settingEnabledProjectTypes)
	}
}

// Every type the create path accepts must be enumerable, otherwise the admin
// UI would offer a whitelist that can never allow it.
func TestAllProjectTypesCoversValidateProject(t *testing.T) {
	for _, pt := range []store.ProjectType{
		store.ProjectTypeDeployment,
		store.ProjectTypeDomainOnly,
		store.ProjectTypeCompose,
		store.ProjectTypeImage,
		store.ProjectTypeBuild,
	} {
		if !store.IsKnownProjectType(pt) {
			t.Errorf("store.AllProjectTypes is missing %q", pt)
		}
	}
	if len(store.AllProjectTypes) != 5 {
		t.Errorf("expected 5 project types, got %d — update the admin UI + i18n too", len(store.AllProjectTypes))
	}
}
