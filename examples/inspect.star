# Read-only inspection; does not publish or change the repository.
print("Top-level entries:", workspace.list_dir(""))
print("Go files:", workspace.glob("**/*.go"))
result = workspace.search("TODO", max_matches=20, output_limit=8192)
for match in result.matches:
    print("{}:{}:{}: {}".format(match.path, match.line, match.column, match.text))
print("Truncated:", result.truncated)
