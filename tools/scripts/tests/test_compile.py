import tempfile
import unittest
from pathlib import Path

SCRIPTS = Path(__file__).resolve().parent.parent


def uncompilable(root: Path) -> list[Path]:
    """Return every .py file under root that does not compile."""
    failed = []
    for path in sorted(root.rglob("*.py")):
        try:
            compile(path.read_bytes(), str(path), "exec")
        except (SyntaxError, ValueError):
            failed.append(path)
    return failed


class CompileTest(unittest.TestCase):
    def test_every_module_under_tools_scripts_compiles(self):
        failed = uncompilable(SCRIPTS)
        self.assertEqual(failed, [], f"does not compile: {[str(p) for p in failed]}")

    def test_the_helper_names_a_file_with_a_syntax_error(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "good.py").write_text("x = 1\n", encoding="utf-8")
            (root / "sub").mkdir()
            (root / "sub" / "bad.py").write_text("def broken(:\n", encoding="utf-8")
            self.assertEqual(uncompilable(root), [root / "sub" / "bad.py"])


if __name__ == "__main__":
    unittest.main()
