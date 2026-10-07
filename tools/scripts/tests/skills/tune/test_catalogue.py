from contextlib import redirect_stderr, redirect_stdout
import io
from pathlib import Path
import tempfile
import unittest
from unittest.mock import call, patch

from skills.tune import catalogue


class CatalogueTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.registry = self.root / "registry.yaml"
        self.registry.write_text("models:\n"
                                 "  shared: {vendor: example, price: [2, 10]}\n"
                                 "  base-only: {vendor: example, price: [1, 5]}\n")

    def test_registry_models_merges_files_and_skips_missing_path(self):
        override = self.root / "override.yaml"
        override.write_text("models:\n"
                            "  shared: {vendor: other, price: [3, 15]}\n"
                            "  override-only: {vendor: other, price: [4, 20]}\n")

        models = catalogue.registry_models([
            self.registry, self.root / "missing.yaml", override,
        ])

        self.assertEqual({
            "shared": {"vendor": "other", "price": "[3, 15]"},
            "base-only": {"vendor": "example", "price": "[1, 5]"},
            "override-only": {"vendor": "other", "price": "[4, 20]"},
        }, models)

    def test_main_without_arguments_returns_one_and_prints_usage_to_stderr(self):
        output, error = io.StringIO(), io.StringIO()

        with redirect_stdout(output), redirect_stderr(error):
            result = catalogue.main([])

        self.assertEqual(1, result)
        self.assertEqual(catalogue.__doc__ + "\n", error.getvalue())
        self.assertEqual("", output.getvalue())

    def test_main_with_registry_prints_fixture_catalogues_and_returns_zero(self):
        self.registry.write_text("models:\n"
                                 "  shared: {vendor: example, price: [2, 10]}\n")
        feeds = {
            catalogue.MODELS_DEV: {"example": {"models": {
                "shared": {"cost": {"input": 3, "output": 15},
                           "limit": {"context": 64000}},
            }}},
            catalogue.OPENROUTER: {"data": [
                {"id": "example/shared", "pricing": {
                    "prompt": "0.000004", "completion": "0.000020",
                }, "context_length": 128000, "created": 2000000000},
                {"id": "example/new-model", "pricing": {
                    "prompt": "0.000001", "completion": "0.000005",
                }, "context_length": 32000, "created": 2000000000},
            ]},
        }
        output, error = io.StringIO(), io.StringIO()

        with patch.object(catalogue, "fetch", side_effect=lambda url: feeds[url]) as fetch, \
                patch.object(catalogue.time, "time", return_value=2000000000), \
                redirect_stdout(output), redirect_stderr(error):
            result = catalogue.main([str(self.registry)])

        self.assertEqual(0, result)
        self.assertEqual("", error.getvalue())
        self.assertEqual([call(catalogue.MODELS_DEV), call(catalogue.OPENROUTER)],
                         fetch.call_args_list)
        lines = output.getvalue().splitlines()
        self.assertEqual("model registry $ models.dev $ openrouter $ ctx(md/or)",
                         " ".join(lines[0].split()))
        self.assertEqual(["shared", "[2,", "10]", "3/15", "4.00/20.00", "64000/128000"],
                         lines[1].split())
        self.assertEqual("On OpenRouter in the last 60 days, not in the registry:", lines[3])
        self.assertEqual(["example/new-model", "1.00/", "5.00", "ctx=32000"], lines[4].split())
        self.assertEqual(5, len(lines))


if __name__ == "__main__":
    unittest.main()
