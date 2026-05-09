package router

import "testing"

// assertRoute is a table-test helper.
func assertRoute(t *testing.T, input, wantModel, wantReason string) {
	t.Helper()
	r := ParseIntent(input)
	if r.Model != wantModel || r.Reason != wantReason {
		t.Errorf("%q\n  got  {%s, %s}\n  want {%s, %s}",
			input, r.Model, r.Reason, wantModel, wantReason)
	}
}

// ── SONNET: planning ──────────────────────────────────────────────────────────

func TestPlanning(t *testing.T) {
	for _, tc := range []string{
		"What's the best architecture for this service?",
		"Suggest a design pattern for the auth module",
		"What's the best approach here?",
		"Should I use microservices or a monolith?",
		"What's the overall strategy for this migration?",
		"How to structure the repository?",
		"Give me an overall plan for the refactor",
	} {
		assertRoute(t, tc, SONNET, "planning")
	}
}

// ── SONNET: multi_file ────────────────────────────────────────────────────────

func TestMultiFile(t *testing.T) {
	for _, tc := range []string{
		"Update all files in the package",
		"Apply this change to every file",
		"Make this consistent across files",
		"This touches multiple files",
		"Refactor the entire codebase",
		"It's a project-wide rename",
		"Refactor all the handlers",
		"Rename everywhere this symbol is used",
	} {
		assertRoute(t, tc, SONNET, "multi_file")
	}
}

// ── SONNET: debugging ─────────────────────────────────────────────────────────

func TestDebugging(t *testing.T) {
	for _, tc := range []string{
		"The server is not working",
		"This doesn't work as expected",
		"It does not work",
		"The build is broken",
		"There's a bug in the handler",
		"Let's debug this function",
		"Why does it return nil here?",
		"Why is this zero?",
		"Why isn't it terminating?",
		"Why doesn't the loop exit?",
		"Got an error in production",
		"The test will fail",
		"The process crashed on startup",
		"The job stalled overnight",
		"I'm stuck on this",
		"The output is wrong",
		"There's an issue in the code",
		"I have a problem with the config",
		"Investigate the slowdown",
		"Diagnose the memory leak",
	} {
		assertRoute(t, tc, SONNET, "debugging")
	}
}

// ── HAIKU: simple_bash ────────────────────────────────────────────────────────

func TestSimpleBash(t *testing.T) {
	cases := []string{
		"Run the test suite",
		"Execute the migration script",
		"Run pytest on the handlers",
		"Use python to seed the database",
	}
	for _, tc := range cases {
		r := ParseIntent(tc)
		if r.Model != HAIKU || r.Reason != "simple_bash" {
			t.Errorf("%q: got {%s, %s}, want {HAIKU, simple_bash}", tc, r.Model, r.Reason)
		}
		if r.Guidance == "" {
			t.Errorf("%q: Guidance must not be empty", tc)
		}
	}
}

// ── HAIKU: simple_search ─────────────────────────────────────────────────────

func TestSimpleSearch(t *testing.T) {
	for _, tc := range []string{
		"Find all usages of that function",
		"Search for TODO comments",
		"Where is this type defined?",
		"Show me the import block",
		"List all test files in the package",
		"Grep for the pattern",
		"Look for the request handler",
	} {
		assertRoute(t, tc, HAIKU, "simple_search")
	}
}

// ── HAIKU: simple_read ───────────────────────────────────────────────────────

func TestSimpleRead(t *testing.T) {
	for _, tc := range []string{
		"Read the config file",
		"Check the middleware logic",
		"What does this function return?",
		"What is the default timeout?",
		"How does the retry logic work?",
	} {
		assertRoute(t, tc, HAIKU, "simple_read")
	}
}

// ── HAIKU: simple_edit ───────────────────────────────────────────────────────

func TestSimpleEdit_VerbPlusExtension(t *testing.T) {
	cases := []string{
		"Fix the typo in main.go",
		"Update the handler in server.py",
		"Edit the styles in app.ts",
		"Modify the helper in utils.js",
		"Replace the constant in config.java",
		"Change the comment in CHANGES.md",
		"Update the note in guide.txt",
		"Fix the method in lib.rb",
	}
	for _, tc := range cases {
		r := ParseIntent(tc)
		if r.Model != HAIKU || r.Reason != "simple_edit" {
			t.Errorf("%q: got {%s, %s}, want {HAIKU, simple_edit}", tc, r.Model, r.Reason)
		}
		if r.Guidance == "" {
			t.Errorf("%q: Guidance must not be empty", tc)
		}
	}
}

func TestSimpleEdit_VerbPlusFilePhrase(t *testing.T) {
	for _, tc := range []string{
		"Fix it in the single file",
		"Change it in one file",
		"Update it in file",
		"Edit this file",
		"Modify the snippet in the file",
	} {
		assertRoute(t, tc, HAIKU, "simple_edit")
	}
}

// Compound rule requires BOTH a verb and a file indicator.

func TestSimpleEdit_VerbAloneIsDefault(t *testing.T) {
	// "fix" alone — no file indicator — falls through to default SONNET.
	assertRoute(t, "fix the function signature", SONNET, "default")
	assertRoute(t, "change the variable name", SONNET, "default")
	assertRoute(t, "update the logic", SONNET, "default")
}

func TestSimpleEdit_FileAloneIsDefault(t *testing.T) {
	// File indicator with no recognized verb and no other keyword.
	assertRoute(t, "the main.go file", SONNET, "default")
	// "look at" ≠ "look for", so no search keyword fires; ".ts" alone without a verb → default.
	assertRoute(t, "look at styles.ts", SONNET, "default")
}

// ── SONNET wins over HAIKU when both could match ──────────────────────────────

func TestSonnetTakesPriority(t *testing.T) {
	// "bug" triggers debugging before "fix" + ".go" can trigger simple_edit.
	assertRoute(t, "fix the bug in main.go", SONNET, "debugging")

	// "error" triggers debugging before "read" triggers simple_read.
	assertRoute(t, "read the error in the log", SONNET, "debugging")

	// "all files" triggers multi_file before "list" triggers simple_search.
	assertRoute(t, "list all files in the repo", SONNET, "multi_file")
}

// ── Default ───────────────────────────────────────────────────────────────────

func TestDefault(t *testing.T) {
	for _, tc := range []string{
		"Hello",
		"What time is it?",
		"Tell me a story",
		"",
	} {
		assertRoute(t, tc, SONNET, "default")
	}
}

// ── RouteTurn delegates to ParseIntent ───────────────────────────────────────

func TestRouteTurn(t *testing.T) {
	for _, tc := range []string{
		"best architecture for the service",
		"run the tests",
		"fix the handler in main.go",
		"",
	} {
		got := RouteTurn(tc)
		want := ParseIntent(tc)
		if got != want {
			t.Errorf("%q: RouteTurn %+v != ParseIntent %+v", tc, got, want)
		}
	}
}

// ── AugmentSystemPrompt ───────────────────────────────────────────────────────

func TestAugmentSystemPrompt(t *testing.T) {
	base := "You are a helpful assistant."
	guidance := "Be concise."

	got := AugmentSystemPrompt(base, guidance)
	want := base + "\n\n**TASK GUIDANCE:**\n" + guidance
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}

	if got := AugmentSystemPrompt(base, ""); got != base {
		t.Errorf("empty guidance: got %q, want %q", got, base)
	}
}
