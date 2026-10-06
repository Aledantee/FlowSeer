import sys
import unittest

from lib import proc


class RunTest(unittest.TestCase):
    def test_shell_syntax_in_an_argument_is_one_argument(self):
        result = proc.run([sys.executable, "-c", "import sys; print(sys.argv[1])", "; echo x"])
        self.assertEqual(result.code, 0)
        self.assertEqual(result.stdout, "; echo x\n")

    def test_exit_code_and_stderr_are_returned(self):
        result = proc.run([sys.executable, "-c", "import sys; sys.stderr.write('bad'); sys.exit(3)"])
        self.assertEqual((result.code, result.stderr), (3, "bad"))

    def test_a_string_is_refused(self):
        with self.assertRaises(TypeError):
            proc.run("echo x")  # type: ignore[arg-type]


if __name__ == "__main__":
    unittest.main()
