package store

import "testing"

func TestValidateDatasetName(t *testing.T) {
	for _, bad := range []string{"", " ", ".", "..", "../../root/.ssh", "a/b", `a\b`, "a:b", "a\nb"} {
		if ValidateDatasetName(bad) == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	for _, ok := range []string{"imagenet", "my-data_v2", "训练集 2026", "a.b"} {
		if err := ValidateDatasetName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
}
