import { describe, expect, it } from "vitest";

import { isTestPath } from "./testPath";

describe("isTestPath", () => {
  it.each([
    "web/src/lib/patch.test.ts",
    "web/src/components/Diff.test.tsx",
    "src/foo.spec.js",
    "e2e/login.e2e.ts",
    "app/login.e2e-spec.ts",
    "src/types.test-d.ts",
    "internal/server/server_test.go",
    "spec/models/user_spec.rb",
    "pkg/foo_test.py",
    "test_parser.py",
    "tools/test_parser.py",
    "conftest.py",
    "web/vitest.setup.ts",
    "jest.setup.js",
    "src/main/java/com/x/FooTest.java",
    "app/FooTests.swift",
    "FooSpec.scala",
    "Lib/WidgetTest.kt",
    "src/__tests__/thing.ts",
    "src/__test__/thing.ts",
    "test/helpers.js",
    "web/src/test/harness.tsx",
    "Tests/AppTests/Helpers.swift",
    "specs/thing.rb",
    "e2e/pages/home.ts",
    "internal/parse/testdata/input.json",
    "src/__snapshots__/Diff.test.tsx.snap",
    "src/Thing.snap",
    "src/__mocks__/fs.ts",
    "src/__fixtures__/a.json",
    "fixtures/payload.json",
    "cypress/support/commands.ts",
  ])("%s is a test", (path) => {
    expect(isTestPath(path)).toBe(true);
  });

  it.each([
    "src/latest.ts",
    "src/main/java/com/x/Latest.java",
    "internal/contest.go",
    "attest.py",
    "testing.md",
    "docs/testing.md",
    "src/testHelpers.ts",
    "src/protest/index.ts",
    "src/contest_view.tsx",
    "src/Testimonial.tsx",
    "src/specification.ts",
    "internal/server/server.go",
    "src/foo.testing.ts",
    "README.md",
    "",
  ])("%s is not a test", (path) => {
    expect(isTestPath(path)).toBe(false);
  });
});
