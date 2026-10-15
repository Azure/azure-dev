# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

import shutil
import tempfile
import unittest
from pathlib import Path

from prepare_preview import prepare_preview


class PreparePreviewTests(unittest.TestCase):
    def test_isolated_idempotent_changes_preserve_identity_and_environment(self):
        with tempfile.TemporaryDirectory() as root:
            shared = Path(root) / "shared"
            shared.mkdir()
            document = (
                "name: shared\nservices:\n"
                "  ai-project:\n    host: azure.ai.project\n    description: keep project\n"
                "  agent:\n    description: old\n    host: azure.ai.agent\n"
                "    name: shared-agent\n    kind: hosted\n"
                "    metadata:\n      tags: [old]\n"
                "    environmentVariables:\n      - name: OLD\n        value: old\n"
                "    env:\n      MODEL: existing-model\n"
                "    codeConfiguration:\n      runtime: python_3_13\n      entryPoint: app.py\n"
                "hooks:\n  predeploy:\n    run: keep hook\n"
            )
            (shared / "azure.yaml").write_text(document, encoding="utf-8")
            (shared / ".azure").mkdir()
            (shared / ".azure" / "config.json").write_text('{"defaultEnvironment":"selected"}')
            copied = Path(root) / "preview"
            shutil.copytree(shared, copied)
            prepare_preview(copied / "azure.yaml", "preview-test")
            result = (copied / "azure.yaml").read_text(encoding="utf-8")
            self.assertEqual(document, (shared / "azure.yaml").read_text(encoding="utf-8"))
            self.assertEqual(
                (shared / ".azure" / "config.json").read_bytes(),
                (copied / ".azure" / "config.json").read_bytes(),
            )
            for retained in ["name: shared-agent", "MODEL: existing-model", "keep project", "keep hook", "app.py"]:
                self.assertIn(retained, result)
            for removed in ["description: old", "tags: [old]", "name: OLD"]:
                self.assertNotIn(removed, result)
            self.assertIn('tags: ["preview-public", "preview-test"]', result)
            self.assertIn("${AZD_PREVIEW_SCENARIO_SECRET}", result)
            self.assertIn('value: "visible-preview-test"', result)
            prepare_preview(copied / "azure.yaml", "preview-test")
            self.assertEqual(result, (copied / "azure.yaml").read_text(encoding="utf-8"))

    def test_unexpected_scaffold_is_not_modified(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "azure.yaml"
            document = "services:\n  agent:\n    kind: prompt\n"
            path.write_text(document, encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "exactly one inline hosted service"):
                prepare_preview(path, "preview-test")
            self.assertEqual(document, path.read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()
