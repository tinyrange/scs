# Inspection is read-only and runs even when the host reports build failure.
print("BUILD-MANIFEST", workspace.read_file(".build-result.json"))
if "hello" in workspace.list_dir(""):
    if workspace.read_file("hello", output_limit=4).content != "\x7fELF":
        fail("compiler output is not ELF")
    if workspace.read_file("program.stdout") != workspace.read_file("expected.txt"):
        fail("executable did not reflect the API edit")
print("INSPECTED-CANDIDATE")
