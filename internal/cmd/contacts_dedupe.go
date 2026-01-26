package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"google.golang.org/api/people/v1"

	"github.com/steipete/gogcli/internal/outfmt"
	"github.com/steipete/gogcli/internal/ui"
)

type ContactsDedupeCmd struct {
	Match string `name:"match" help:"Match fields: email,phone,name" default:"email,phone,name"`
	Max   int64  `name:"max" aliases:"limit" help:"Max contacts to scan (0 = all)" default:"0"`
	Apply bool   `name:"apply" help:"Apply merge/delete operations"`
}

func (c *ContactsDedupeCmd) Run(ctx context.Context, flags *RootFlags) error {
	u := ui.FromContext(ctx)
	account, err := requireAccount(flags)
	if err != nil {
		return err
	}

	match, err := parseDedupeMatch(c.Match)
	if err != nil {
		return err
	}

	svc, err := newPeopleContactsService(ctx, account)
	if err != nil {
		return err
	}

	contacts, err := listContacts(ctx, svc, c.Max)
	if err != nil {
		return err
	}

	groups := buildDedupeGroups(contacts, match)
	if err := outputDedupeGroups(ctx, u, groups); err != nil {
		return err
	}
	if !c.Apply {
		return nil
	}
	if len(groups) == 0 {
		return nil
	}

	if err := confirmDestructive(ctx, flags, fmt.Sprintf("merge %d contact groups", len(groups))); err != nil {
		return err
	}

	for _, g := range groups {
		merged := mergeContactGroup(g)
		_, err := svc.People.UpdateContact(g.Primary.ResourceName, merged).
			UpdatePersonFields("names,emailAddresses,phoneNumbers").
			Do()
		if err != nil {
			return err
		}
		for _, m := range g.Members {
			if m.ResourceName == g.Primary.ResourceName {
				continue
			}
			if _, err := svc.People.DeleteContact(m.ResourceName).Do(); err != nil {
				return err
			}
		}
	}
	return nil
}

type dedupeMatch struct {
	Email bool
	Phone bool
	Name  bool
}

func parseDedupeMatch(value string) (dedupeMatch, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return dedupeMatch{}, usage("empty --match")
	}
	out := dedupeMatch{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		switch part {
		case "email":
			out.Email = true
		case "phone":
			out.Phone = true
		case "name":
			out.Name = true
		case "":
			continue
		default:
			return dedupeMatch{}, usagef("invalid --match %q (use email,phone,name)", part)
		}
	}
	if !out.Email && !out.Phone && !out.Name {
		return dedupeMatch{}, usage("invalid --match (no fields enabled)")
	}
	return out, nil
}

func listContacts(ctx context.Context, svc *people.Service, max int64) ([]*people.Person, error) {
	out := make([]*people.Person, 0, 128)
	var pageToken string
	for {
		pageSize := int64(500)
		if max > 0 && max < pageSize {
			pageSize = max
		}
		call := svc.People.Connections.List(peopleMeResource).
			PersonFields(contactsReadMask).
			PageSize(pageSize).
			PageToken(pageToken).
			RequestSyncToken(false)
		resp, err := call.Do()
		if err != nil {
			return nil, err
		}
		for _, p := range resp.Connections {
			if p == nil {
				continue
			}
			out = append(out, p)
			if max > 0 && int64(len(out)) >= max {
				return out, nil
			}
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return out, nil
}

type dedupeGroup struct {
	Primary *people.Person
	Members []*people.Person
	Merged  contactSummary
}

type contactSummary struct {
	Resource string   `json:"resource"`
	Name     string   `json:"name,omitempty"`
	Emails   []string `json:"emails,omitempty"`
	Phones   []string `json:"phones,omitempty"`
}

func buildDedupeGroups(contacts []*people.Person, match dedupeMatch) []dedupeGroup {
	if len(contacts) == 0 {
		return nil
	}
	uf := newUnionFind(len(contacts))
	seen := map[string]int{}

	for i, p := range contacts {
		keys := contactKeys(p, match)
		for _, key := range keys {
			if j, ok := seen[key]; ok {
				uf.union(i, j)
			} else {
				seen[key] = i
			}
		}
	}

	groups := map[int][]*people.Person{}
	for i, p := range contacts {
		root := uf.find(i)
		groups[root] = append(groups[root], p)
	}

	out := make([]dedupeGroup, 0)
	for _, members := range groups {
		if len(members) < 2 {
			continue
		}
		primary := choosePrimaryContact(members)
		out = append(out, dedupeGroup{
			Primary: primary,
			Members: members,
			Merged:  summarizeMergedContact(primary, members),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Primary.ResourceName < out[j].Primary.ResourceName
	})
	return out
}

func contactKeys(p *people.Person, match dedupeMatch) []string {
	if p == nil {
		return nil
	}
	keys := make([]string, 0, 4)
	if match.Email {
		for _, e := range p.EmailAddresses {
			if e == nil {
				continue
			}
			if v := normalizeEmail(e.Value); v != "" {
				keys = append(keys, "email:"+v)
			}
		}
	}
	if match.Phone {
		for _, ph := range p.PhoneNumbers {
			if ph == nil {
				continue
			}
			if v := normalizePhone(ph.Value); v != "" {
				keys = append(keys, "phone:"+v)
			}
		}
	}
	if match.Name {
		if v := normalizeName(primaryName(p)); v != "" {
			keys = append(keys, "name:"+v)
		}
	}
	return keys
}

func choosePrimaryContact(members []*people.Person) *people.Person {
	if len(members) == 0 {
		return nil
	}
	best := members[0]
	bestScore := contactScore(best)
	for _, m := range members[1:] {
		if m == nil {
			continue
		}
		score := contactScore(m)
		if score > bestScore {
			best = m
			bestScore = score
		} else if score == bestScore && m.ResourceName < best.ResourceName {
			best = m
		}
	}
	return best
}

func contactScore(p *people.Person) int {
	if p == nil {
		return 0
	}
	score := 0
	if primaryName(p) != "" {
		score += 2
	}
	score += len(p.EmailAddresses) * 2
	score += len(p.PhoneNumbers) * 2
	return score
}

func summarizeMergedContact(primary *people.Person, members []*people.Person) contactSummary {
	merged := mergeContactGroup(dedupeGroup{Primary: primary, Members: members})
	return contactSummary{
		Resource: merged.ResourceName,
		Name:     primaryName(merged),
		Emails:   uniqueEmails(merged.EmailAddresses),
		Phones:   uniquePhones(merged.PhoneNumbers),
	}
}

func mergeContactGroup(group dedupeGroup) *people.Person {
	primary := group.Primary
	if primary == nil {
		return &people.Person{}
	}
	name := primaryName(primary)
	nameSource := primary
	emails := make([]*people.EmailAddress, 0)
	phones := make([]*people.PhoneNumber, 0)

	seenEmails := map[string]bool{}
	seenPhones := map[string]bool{}

	addEmail := func(value string) {
		normalized := normalizeEmail(value)
		if normalized == "" || seenEmails[normalized] {
			return
		}
		seenEmails[normalized] = true
		emails = append(emails, &people.EmailAddress{Value: strings.TrimSpace(value)})
	}
	addPhone := func(value string) {
		normalized := normalizePhone(value)
		if normalized == "" || seenPhones[normalized] {
			return
		}
		seenPhones[normalized] = true
		phones = append(phones, &people.PhoneNumber{Value: strings.TrimSpace(value)})
	}

	for _, p := range orderedMembers(primary, group.Members) {
		if p == nil {
			continue
		}
		if name == "" {
			if n := primaryName(p); n != "" {
				name = n
				nameSource = p
			}
		}
		for _, e := range p.EmailAddresses {
			if e == nil {
				continue
			}
			addEmail(e.Value)
		}
		for _, ph := range p.PhoneNumbers {
			if ph == nil {
				continue
			}
			addPhone(ph.Value)
		}
	}

	merged := *primary
	if name != "" {
		if nameSource == primary && len(primary.Names) > 0 {
			merged.Names = primary.Names
		} else {
			merged.Names = []*people.Name{{DisplayName: name}}
		}
	}
	if len(emails) > 0 {
		merged.EmailAddresses = emails
	}
	if len(phones) > 0 {
		merged.PhoneNumbers = phones
	}
	return &merged
}

func orderedMembers(primary *people.Person, members []*people.Person) []*people.Person {
	if primary == nil || len(members) <= 1 {
		return members
	}
	out := make([]*people.Person, 0, len(members))
	out = append(out, primary)
	for _, m := range members {
		if m == nil || m.ResourceName == primary.ResourceName {
			continue
		}
		out = append(out, m)
	}
	return out
}

func outputDedupeGroups(ctx context.Context, u *ui.UI, groups []dedupeGroup) error {
	if outfmt.IsJSON(ctx) {
		out := make([]map[string]any, 0, len(groups))
		for _, g := range groups {
			members := make([]contactSummary, 0, len(g.Members))
			for _, m := range g.Members {
				members = append(members, summarizeContact(m))
			}
			out = append(out, map[string]any{
				"primary": summarizeContact(g.Primary),
				"merged":  g.Merged,
				"members": members,
			})
		}
		return outfmt.WriteJSON(os.Stdout, map[string]any{"groups": out})
	}

	if len(groups) == 0 {
		if u != nil {
			u.Err().Println("No duplicates")
		}
		return nil
	}

	w, flush := tableWriter(ctx)
	defer flush()
	fmt.Fprintln(w, "GROUP\tACTION\tRESOURCE\tNAME\tEMAIL\tPHONE")
	for i, g := range groups {
		for _, m := range g.Members {
			action := "merge"
			if g.Primary != nil && m.ResourceName == g.Primary.ResourceName {
				action = "keep"
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
				i+1,
				action,
				m.ResourceName,
				sanitizeTab(primaryName(m)),
				sanitizeTab(primaryEmail(m)),
				sanitizeTab(primaryPhone(m)),
			)
		}
	}
	return nil
}

func summarizeContact(p *people.Person) contactSummary {
	if p == nil {
		return contactSummary{}
	}
	return contactSummary{
		Resource: p.ResourceName,
		Name:     primaryName(p),
		Emails:   uniqueEmails(p.EmailAddresses),
		Phones:   uniquePhones(p.PhoneNumbers),
	}
}

func uniqueEmails(list []*people.EmailAddress) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(list))
	for _, e := range list {
		if e == nil {
			continue
		}
		normalized := normalizeEmail(e.Value)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, strings.TrimSpace(e.Value))
	}
	return out
}

func uniquePhones(list []*people.PhoneNumber) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(list))
	for _, p := range list {
		if p == nil {
			continue
		}
		normalized := normalizePhone(p.Value)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, strings.TrimSpace(p.Value))
	}
	return out
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizePhone(value string) string {
	out := make([]rune, 0, len(value))
	for _, r := range value {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}
	return string(out)
}

func normalizeName(value string) string {
	parts := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	return strings.Join(parts, " ")
}

type unionFind struct {
	parent []int
	rank   []int
}

func newUnionFind(n int) *unionFind {
	parent := make([]int, n)
	rank := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	return &unionFind{parent: parent, rank: rank}
}

func (u *unionFind) find(x int) int {
	if u.parent[x] != x {
		u.parent[x] = u.find(u.parent[x])
	}
	return u.parent[x]
}

func (u *unionFind) union(a int, b int) {
	ra := u.find(a)
	rb := u.find(b)
	if ra == rb {
		return
	}
	if u.rank[ra] < u.rank[rb] {
		u.parent[ra] = rb
		return
	}
	if u.rank[ra] > u.rank[rb] {
		u.parent[rb] = ra
		return
	}
	u.parent[rb] = ra
	u.rank[ra]++
}
