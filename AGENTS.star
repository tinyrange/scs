workspace = privileged.workspace(".", readonly=False)
def propose_agents_star(content):
    """Stage a full configuration and request user approval before installing it."""
    candidate = privileged.tempdir()
    path = candidate.path("candidate.star")
    privileged.write_file(path, content)
    privileged.edit_agents_star(path)

load("//stdlib/golang.star", go_tools="tools", go_test="test", go_run="run")
environment = {"workspace": workspace, "propose_agents_star": propose_agents_star, "go": go_tools, "go_test": go_test, "go_run": go_run}
default_repl = repl(environment)
default = privileged.model("gpt-6-astra").create(default_repl, prompt_addons=[])
