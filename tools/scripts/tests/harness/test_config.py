import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from harness.config import load
from tests.harness import configuration


class ConfigTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.path = configuration(self.root)

    def test_missing_field(self):
        self.path.write_text(self.path.read_text().replace("allowed_user_id = 7\n", ""))
        with self.assertRaisesRegex(ValueError, "allowed_user_id"):
            load(self.path)

    def test_public_token_file(self):
        (self.root / "credential").chmod(0o644)
        with self.assertRaisesRegex(ValueError, "permissions"):
            load(self.path)

    def test_valid_configuration_and_environment(self):
        with patch.dict(os.environ, {"FLOWSEER_HARNESS_CONFIG": str(self.path)}):
            config = load()
        self.assertEqual((config.allowed_user_id, config.private_chat_id), (7, 11))
        self.assertEqual((config.default.agent, config.default.model), ("claude", "default-model"))
        self.assertEqual((config.alternatives[0].agent, config.alternatives[0].model), ("codex", "gpt-5.5"))
        self.assertEqual(config.state_dir, (self.root / "state").resolve())
        self.assertNotIn("dummy-fixture-credential", repr(config))

    def test_checkout_paths_refused(self):
        checkout = Path(__file__).resolve().parents[4]
        for field, value in (("state_dir", checkout / "state"), ("bot_token_file", checkout / "credential")):
            with self.subTest(field=field):
                configuration(self.root)
                lines = self.path.read_text().splitlines()
                self.path.write_text("\n".join(f'{field} = "{value}"' if line.startswith(field + " =") else line for line in lines))
                with self.assertRaisesRegex(ValueError, "outside the checkout"):
                    load(self.path)

    def test_invalid_ids_and_models(self):
        for before, after in (("allowed_user_id = 7", "allowed_user_id = true"), ('model = "default-model"', 'model = ""')):
            with self.subTest(after=after):
                configuration(self.root)
                self.path.write_text(self.path.read_text().replace(before, after))
                with self.assertRaises(ValueError):
                    load(self.path)
