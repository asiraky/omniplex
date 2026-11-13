// Directories that hold tests, or what only tests read. Matched in any case:
// Swift packages put theirs under `Tests/`.
const DIRS = new Set([
  "__tests__",
  "__test__",
  "test",
  "tests",
  "spec",
  "specs",
  "e2e",
  "testdata",
  "__snapshots__",
  "__mocks__",
  "__fixtures__",
  "fixtures",
  "cypress",
]);

// File names that say "test" by convention, one ecosystem per line. Each
// needs a separator or a case change before the word, so `latest.ts`,
// `contest.go` and `attest.py` stay ordinary source.
const NAMES = [
  /\.(test|spec|e2e)(-[a-z]+)?\.[^/]+$/, // foo.test.ts, foo.spec.tsx, foo.e2e-spec.ts, foo.test-d.ts
  /_(test|spec)\.[^./]+$/, // foo_test.go, foo_spec.rb, foo_test.py
  /^test_[^/]*\.py$/, // test_foo.py
  /^conftest\.py$/,
  /^(vitest|jest)\.setup\.[^/]+$/,
  /\.snap$/,
  // FooTest.java, FooTests.swift, FooSpec.scala. Case-sensitive on purpose:
  // the capital T is all that tells FooTest.java from Latest.java.
  /[A-Za-z0-9_](Test|Tests|Spec)\.(java|kt|kts|cs|swift|php|scala|groovy|m|mm)$/,
];

/**
 * Whether a changed file is a test, or something only tests read: a test
 * file by its name, or any file under a test directory. Errs towards false;
 * hiding real source behind "hide tests" is the worse mistake.
 */
export function isTestPath(path: string): boolean {
  const parts = path.split(/[\\/]/).filter(Boolean);
  const name = parts.pop();
  if (name === undefined) return false;
  if (parts.some((dir) => DIRS.has(dir.toLowerCase()))) return true;
  return NAMES.some((re) => re.test(name));
}
