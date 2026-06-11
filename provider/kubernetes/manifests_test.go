package kubernetes

import (
	"testing"
)

func TestParseManifests(t *testing.T) {
	docs := []string{
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm1\ndata:\n  k: v\n",
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: dep1\n",
	}

	objs, err := parseManifests(docs)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(objs) != 2 {
		t.Fatalf("objs = %d, want 2", len(objs))
	}

	if objs[0].GetKind() != "ConfigMap" || objs[0].GetName() != "cm1" {
		t.Errorf("obj0 = %s/%s", objs[0].GetKind(), objs[0].GetName())
	}

	if objs[1].GetAPIVersion() != "apps/v1" || objs[1].GetName() != "dep1" {
		t.Errorf("obj1 = %s %s", objs[1].GetAPIVersion(), objs[1].GetName())
	}
}

func TestParseManifests_MissingKind(t *testing.T) {
	if _, err := parseManifests([]string{"apiVersion: v1\nmetadata:\n  name: x\n"}); err == nil {
		t.Fatal("expected error for manifest without kind")
	}
}
