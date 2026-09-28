# Run once against a fresh import. main stays unchanged; two forks are published.
# The example uses a new directory to avoid depending on the imported project.
a = workspace.fork()
b = workspace.fork()

for child in [a, b]:
    child.mkdir("scs-agent-example")

a.write_file("scs-agent-example/result.txt", "Result from agent A\n")
b.write_file("scs-agent-example/result.txt", "Result from agent B\n")

a.publish("agent-a")
b.publish("agent-b")
print("Published independent agent-a and agent-b workspaces; main is unchanged.")
