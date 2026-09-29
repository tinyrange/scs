# Run once in a newly initialized repository, before creating any candidate.
workspace.write_file("hello.c", """#include <stdio.h>
#ifdef FORCE_FAILURE
#error requested compiler failure
#endif
int main(void) {
    puts("original");
    return 0;
}
""")
workspace.write_file("expected.txt", "original\n")
