// SPDX-License-Identifier: Apache-2.0

package auditrecord

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestPageCursorRoundTripIsCanonicalAndWorkspaceBound(t *testing.T) {
	t.Parallel()
	cursor, err := EncodePageCursor("workspace-a", 513, hash("f"), 513, hash("e"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cursor) != PageCursorChars || strings.ContainsAny(cursor, "+/=") {
		t.Fatalf("cursor = %q", cursor)
	}
	request, err := ContinuePage("workspace-a", cursor)
	if err != nil {
		t.Fatal(err)
	}
	if request.Initial() || request.HeadSequence() != 513 || request.HeadHash() != hash("f") ||
		request.NextSequence() != 513 || request.PreviousHash() != hash("e") {
		t.Fatalf("request = %#v", request)
	}
	if _, err := ContinuePage("workspace-b", cursor); err == nil {
		t.Fatal("foreign workspace accepted cursor")
	}
	first, err := FirstPage("workspace-a")
	if err != nil || !first.Initial() || first.ValidateForWorkspace("workspace-a") != nil ||
		first.ValidateForWorkspace("workspace-b") == nil {
		t.Fatalf("first page request/error = %#v/%v", first, err)
	}
}

func TestPageRequestBindsReturnedPage(t *testing.T) {
	t.Parallel()
	pages := validTestPages(t, MaxEntries+1)
	first, err := FirstPage("workspace-a")
	if err != nil || first.ValidatePage(pages[0]) != nil {
		t.Fatalf("first request/page error = %v", err)
	}
	continuation, err := ContinuePage("workspace-a", pages[0].NextCursor)
	if err != nil || continuation.ValidatePage(pages[1]) != nil {
		t.Fatalf("continuation request/page error = %v", err)
	}
	if continuation.ValidatePage(pages[0]) == nil || first.ValidatePage(pages[1]) == nil {
		t.Fatal("page request accepted the wrong interval")
	}
}

func TestPageCursorRejectsMalformedAndNonCanonicalEncodings(t *testing.T) {
	t.Parallel()
	cursor, err := EncodePageCursor("workspace-a", 513, hash("f"), 513, hash("e"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]string{
		"short":            cursor[:len(cursor)-1],
		"padded":           cursor + "=",
		"invalid alphabet": strings.Repeat("+", PageCursorChars),
		"unknown version":  base64.RawURLEncoding.EncodeToString(append([]byte{2}, payload[1:]...)),
	}
	for name, encoded := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ContinuePage("workspace-a", encoded); err == nil {
				t.Fatal("invalid cursor was accepted")
			}
		})
	}
	for _, test := range []struct {
		head, next             int64
		headHash, previousHash string
	}{
		{head: 1, next: 1, headHash: hash("a"), previousHash: hash("0")},
		{head: 513, next: 514, headHash: hash("a"), previousHash: hash("b")},
		{head: 513, next: 513, headHash: "bad", previousHash: hash("b")},
	} {
		if _, err := EncodePageCursor("workspace-a", test.head, test.headHash, test.next, test.previousHash); err == nil {
			t.Fatalf("invalid boundary accepted: %#v", test)
		}
	}
}

func TestPageVerifyAndSequenceVerifierAcceptCompleteSnapshot(t *testing.T) {
	t.Parallel()
	pages := validTestPages(t, MaxEntries+1)
	if len(pages) != 2 || pages[0].NextCursor == "" || pages[1].NextCursor != "" {
		t.Fatalf("pages = %#v", pages)
	}
	for index, page := range pages {
		if err := page.Verify(); err != nil {
			t.Fatalf("page %d Verify() error = %v", index, err)
		}
	}
	var verifier PageSequenceVerifier
	for _, page := range pages {
		if err := verifier.Add(page); err != nil {
			t.Fatal(err)
		}
	}
	result, err := verifier.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if result.Schema != PageSchemaV1 || result.WorkspaceID != "workspace-a" || result.Pages != 2 ||
		result.Entries != MaxEntries+1 || result.Chain != pages[0].Snapshot {
		t.Fatalf("result = %#v", result)
	}
	if err := verifier.Add(pages[1]); err == nil {
		t.Fatal("complete verifier accepted another page")
	}
}

func TestPageSequenceVerifierRejectsIncompleteReorderedAndForeignPages(t *testing.T) {
	t.Parallel()
	pages := validTestPages(t, MaxEntries+1)
	var incomplete PageSequenceVerifier
	if err := incomplete.Add(pages[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := incomplete.Finish(); err == nil {
		t.Fatal("incomplete sequence verified")
	}

	for name, candidate := range map[string][]Page{
		"starts after genesis": {pages[1]},
		"reordered":            {pages[1], pages[0]},
		"replayed":             {pages[0], pages[0]},
		"missing first link": func() []Page {
			changed := pages[0]
			changed.PreviousHash = hash("a")
			return []Page{changed}
		}(),
		"foreign workspace": func() []Page {
			changed := pages[0]
			changed.WorkspaceID = "workspace-b"
			return []Page{changed}
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			var verifier PageSequenceVerifier
			for _, page := range candidate {
				if err := verifier.Add(page); err != nil {
					return
				}
			}
			if _, err := verifier.Finish(); err == nil {
				t.Fatal("invalid sequence verified")
			}
		})
	}
}

func TestPageVerifyRejectsTamperedRangeCursorAndHash(t *testing.T) {
	t.Parallel()
	base := validTestPages(t, MaxEntries+1)[0]
	mutations := map[string]func(*Page){
		"workspace":      func(page *Page) { page.WorkspaceID = "workspace-b" },
		"start":          func(page *Page) { page.StartSequence = 2 },
		"previous":       func(page *Page) { page.PreviousHash = hash("a") },
		"entry":          func(page *Page) { page.Entries[0].Actor = "user:mallory" },
		"head":           func(page *Page) { page.Snapshot.HeadHash = hash("b") },
		"missing cursor": func(page *Page) { page.NextCursor = "" },
		"cursor":         func(page *Page) { page.NextCursor = strings.Repeat("A", PageCursorChars) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			page := base
			page.Entries = append([]Entry(nil), base.Entries...)
			mutate(&page)
			if err := page.Verify(); err == nil {
				t.Fatal("tampered page verified")
			}
		})
	}
}

func FuzzPageCursorMutationCannotBindOriginalPage(f *testing.F) {
	f.Add(uint8(0), uint8(1))
	f.Add(uint8(41), uint8(0xff))
	f.Add(uint8(112), uint8(0x80))
	f.Fuzz(func(t *testing.T, offset, delta uint8) {
		cursor, page := validContinuationPage(t)
		payload, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			t.Fatal(err)
		}
		change := delta
		if change == 0 {
			change = 1
		}
		payload[int(offset)%len(payload)] ^= change
		request, err := ContinuePage("workspace-a", base64.RawURLEncoding.EncodeToString(payload))
		if err != nil {
			return
		}
		if request.ValidatePage(page) == nil {
			t.Fatal("mutated continuation bound the original page")
		}
	})
}

func validContinuationPage(t *testing.T) (string, Page) {
	t.Helper()
	previous := hash("e")
	entry := Entry{
		Sequence: 513, FormatVersion: 1,
		RecordedAt: time.Date(2026, time.July, 18, 10, 0, 0, 0, time.UTC),
		TraceID:    strings.Repeat("1", 32), Actor: "user:alice", Role: "admin", Action: "export-audit",
		Verb: "audit.export", Verdict: "allow", ReasonCode: "phase-1-audit-export",
		EventKind: "policy-decision", PreviousHash: previous,
	}
	head, err := RecomputeEntryHash("workspace-a", entry)
	if err != nil {
		t.Fatal(err)
	}
	entry.EntryHash = head
	page := Page{
		Schema: PageSchemaV1, WorkspaceID: "workspace-a",
		Snapshot:      Chain{HashAlgorithm: HashAlgorithm, HeadSequence: entry.Sequence, HeadHash: head},
		StartSequence: entry.Sequence, PreviousHash: previous, Entries: []Entry{entry},
	}
	cursor, err := EncodePageCursor("workspace-a", entry.Sequence, head, entry.Sequence, previous)
	if err != nil {
		t.Fatal(err)
	}
	return cursor, page
}

func validTestPages(t *testing.T, count int) []Page {
	t.Helper()
	entries := make([]Entry, count)
	previous := zeroHash()
	for index := range entries {
		entry := Entry{
			Sequence: int64(index + 1), FormatVersion: 1,
			RecordedAt: time.Date(2026, time.July, 18, 10, 0, 0, index*1000, time.UTC).Truncate(time.Microsecond),
			TraceID:    strings.Repeat("1", 32), Actor: "user:alice", Role: "admin", Action: "export-audit",
			Verb: "audit.export", Verdict: "allow", ReasonCode: "phase-1-audit-export",
			EventKind: "policy-decision", PreviousHash: previous,
		}
		hashValue, err := RecomputeEntryHash("workspace-a", entry)
		if err != nil {
			t.Fatal(err)
		}
		entry.EntryHash = hashValue
		entries[index] = entry
		previous = hashValue
	}
	snapshot := Chain{HashAlgorithm: HashAlgorithm, HeadSequence: int64(count), HeadHash: previous}
	pages := make([]Page, 0, (count+MaxEntries-1)/MaxEntries)
	for start := 0; start < count; start += MaxEntries {
		end := start + MaxEntries
		if end > count {
			end = count
		}
		page := Page{
			Schema: PageSchemaV1, WorkspaceID: "workspace-a", Snapshot: snapshot,
			StartSequence: int64(start + 1), PreviousHash: entries[start].PreviousHash,
			Entries: append([]Entry(nil), entries[start:end]...),
		}
		if end < count {
			cursor, err := EncodePageCursor("workspace-a", int64(count), previous, int64(end+1), entries[end-1].EntryHash)
			if err != nil {
				t.Fatal(err)
			}
			page.NextCursor = cursor
		}
		pages = append(pages, page)
	}
	return pages
}
