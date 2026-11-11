# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[5]
EXTENSIONS = ROOT / "cli" / "azd" / "extensions"
INDEX = EXTENSIONS / "skills" / "README.md"
EXTENSION_NAMES = ("agents", "connections", "inspector", "projects", "routines", "skills", "toolboxes")
SKILL_NAMES = (
    "foundry-extension-scenario-authoring",
    "foundry-extension-scenario-pr-regression",
    "foundry-extension-scenario-suite-run",
)
AGENT_NAMES = (
    "foundry-extension-scenario-author",
    "foundry-extension-scenario-orchestrator",
    "foundry-extension-scenario-worker",
)
LINK = re.compile(r"\]\(([^)\s]+)\)")


def read(path):
    return path.read_text(encoding="utf-8")


def header_and_body(path):
    match = re.fullmatch(r"---\n(.*?)\n---\n(.*)", read(path), re.DOTALL)
    if not match:
        raise ValueError(f"Missing frontmatter: {path}")
    return match.groups()


def local_targets(path):
    for link in LINK.findall(read(path)):
        if re.match(r"[a-z]+:|#|/", link):
            continue
        yield (path.parent / link.split("#", 1)[0]).resolve()


class RoutingTests(unittest.TestCase):
    def test_root_dispatcher_points_to_extension_index(self):
        entry = ROOT / ".github" / "skills" / "azd-extensions" / "SKILL.md"
        header, body = header_and_body(entry)
        self.assertIn("name: azd-extensions", header)
        self.assertIn("description:", header)
        self.assertEqual(list(local_targets(entry)), [INDEX])
        self.assertIn("Stop", body)
        self.assertIn("If no workflow matches", body)
        self.assertIn("explicit authorization", read(INDEX))

    def test_skill_compatibility_entry_points(self):
        for name in SKILL_NAMES:
            with self.subTest(skill=name):
                entry = ROOT / ".github" / "skills" / name / "SKILL.md"
                header, body = header_and_body(entry)
                expected = EXTENSIONS / "azure.ai.agents" / "skills" / name / "workflow.md"
                self.assertIn(f"name: {name}", header)
                self.assertIn("description:", header)
                self.assertEqual(list(local_targets(entry)), [expected])
                self.assertIn("Before acting, read and follow", body)
                self.assertIn("Stop", body)
                self.assertLess(len(body.splitlines()), 15)

    def test_agent_invocation_and_tool_boundaries_are_preserved(self):
        for name in AGENT_NAMES:
            with self.subTest(agent=name):
                entry = ROOT / ".github" / "agents" / f"{name}.agent.md"
                header, body = header_and_body(entry)
                expected = EXTENSIONS / "azure.ai.agents" / "agents" / f"{name}.md"
                self.assertIn(f"name: {name}", header)
                self.assertIn("disable-model-invocation: true", header)
                self.assertEqual(list(local_targets(entry)), [expected])
                self.assertIn("Stop", body)
                self.assertLess(len(body.splitlines()), 15)
                if name.endswith("-worker"):
                    tools = re.findall(r"^  - ([^\s#]+)", header, re.MULTILINE)
                    self.assertEqual(tools, ["cli-interactive-tester/*", "read", "todo"])

    def test_extension_index_registers_workflows_and_roles(self):
        targets = set(local_targets(INDEX))
        registered_workflows = {path for path in targets if path.name == "workflow.md"}
        on_disk = set((EXTENSIONS / "skills").glob("*/workflow.md"))
        for name in EXTENSION_NAMES:
            extension = EXTENSIONS / f"azure.ai.{name}"
            on_disk.update((extension / "skills").glob("*/workflow.md"))
        self.assertTrue(registered_workflows)
        self.assertEqual(registered_workflows, on_disk)
        for name in AGENT_NAMES:
            self.assertIn(EXTENSIONS / "azure.ai.agents" / "agents" / f"{name}.md", targets)

    def test_local_links_resolve_inside_repository(self):
        documents = [INDEX]
        documents.extend((ROOT / ".github" / "skills").glob("foundry-extension-scenario-*/SKILL.md"))
        documents.append(ROOT / ".github" / "skills" / "azd-extensions" / "SKILL.md")
        documents.extend((ROOT / ".github" / "agents").glob("foundry-extension-scenario-*.agent.md"))
        documents.extend((EXTENSIONS / "skills").glob("*/workflow.md"))
        documents.extend((EXTENSIONS / "azure.ai.agents" / "skills").rglob("*.md"))
        documents.extend((EXTENSIONS / "azure.ai.agents" / "agents").glob("*.md"))
        for document in documents:
            for target in local_targets(document):
                with self.subTest(source=document, target=target):
                    self.assertTrue(target.is_relative_to(ROOT))
                    self.assertTrue(target.is_file(), f"Missing target from {document}: {target}")

    def test_moved_reference_documents_are_explicitly_loaded(self):
        directory = EXTENSIONS / "azure.ai.agents" / "skills" / SKILL_NAMES[1]
        workflow = directory / "workflow.md"
        targets = set(local_targets(workflow))
        self.assertEqual(targets & set((directory / "references").glob("*.md")),
                         set((directory / "references").glob("*.md")))
        for document in (EXTENSIONS / "azure.ai.agents" / "skills").rglob("*.md"):
            text = read(document)
            self.assertNotIn("{{ references/", text)
            self.assertNotIn(".github/skills/foundry-extension-scenario-", text)


if __name__ == "__main__":
    unittest.main()
