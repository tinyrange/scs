# The compiled program must reflect this native API edit, not the source root.
workspace.replace("hello.c", 'puts("original")', 'puts("edited through native API")')
workspace.write_file("expected.txt", "edited through native API\n")
