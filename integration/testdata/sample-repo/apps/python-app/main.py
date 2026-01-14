import os

def main():
    repo_root = os.getenv("REPO_ROOT", "")
    bundle_root = os.getenv("BUNDLE_ROOT", "")
    python_var = os.getenv("PYTHON_VAR", "")

    print(f"REPO_ROOT={repo_root}")
    print(f"BUNDLE_ROOT={bundle_root}")
    print(f"PYTHON_VAR={python_var}")

if __name__ == "__main__":
    main()
