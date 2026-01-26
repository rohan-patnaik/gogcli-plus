package cmd

import (
	"testing"

	"google.golang.org/api/people/v1"
)

func TestParseDedupeMatch(t *testing.T) {
	if _, err := parseDedupeMatch("email,phone,name"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := parseDedupeMatch("bad"); err == nil {
		t.Fatalf("expected error for invalid match")
	}
}

func TestNormalizePhone(t *testing.T) {
	got := normalizePhone("(415) 555-1212")
	if got != "4155551212" {
		t.Fatalf("normalizePhone = %q", got)
	}
}

func TestBuildDedupeGroups(t *testing.T) {
	p1 := person("people/1", "Alice A", "alice@example.com", "")
	p2 := person("people/2", "Alice A", "ALICE@example.com", "")
	p3 := person("people/3", "Bob B", "bob@example.com", "")

	match, _ := parseDedupeMatch("email")
	groups := buildDedupeGroups([]*people.Person{p1, p2, p3}, match)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if len(groups[0].Members) != 2 {
		t.Fatalf("group members = %d, want 2", len(groups[0].Members))
	}
}

func TestMergeContactGroup(t *testing.T) {
	primary := person("people/1", "Alice A", "alice@example.com", "")
	other := person("people/2", "", "alice+alt@example.com", "123")
	group := dedupeGroup{Primary: primary, Members: []*people.Person{primary, other}}

	merged := mergeContactGroup(group)
	if len(merged.EmailAddresses) != 2 {
		t.Fatalf("emails = %d, want 2", len(merged.EmailAddresses))
	}
	if len(merged.PhoneNumbers) != 1 {
		t.Fatalf("phones = %d, want 1", len(merged.PhoneNumbers))
	}
}

func person(resource string, name string, email string, phone string) *people.Person {
	p := &people.Person{ResourceName: resource}
	if name != "" {
		p.Names = []*people.Name{{DisplayName: name}}
	}
	if email != "" {
		p.EmailAddresses = []*people.EmailAddress{{Value: email}}
	}
	if phone != "" {
		p.PhoneNumbers = []*people.PhoneNumber{{Value: phone}}
	}
	return p
}
