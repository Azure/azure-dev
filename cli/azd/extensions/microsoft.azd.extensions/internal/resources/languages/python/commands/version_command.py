import os
import importlib.util
from rich.console import Console

class VersionCommand:
    def __init__(self, azd_client):
        self.azd_client = azd_client
        self.console = Console()

    async def execute(self):
        try:
            version_info = self._get_version_info()

            # Print version information
            self.console.print(f"[bold green]Wallace.py extension[/bold green] version [bold]{version_info['version']}[/bold]")

            # If we have additional build information, display it
            if 'commit' in version_info and version_info['commit']:
                self.console.print(f"Commit: {version_info['commit']}")
            if 'build_date' in version_info and version_info['build_date']:
                self.console.print(f"Build date: {version_info['build_date']}")

        except Exception as e:
            self.console.print(f"[bold red]Error reading version information:[/bold red] {str(e)}")

    def _get_version_info(self):
        """Get version information from the embedded version module"""
        info = {"version": "unknown"}

        try:
            # Try to import the version module
            base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
            version_py_path = os.path.join(base_dir, "version.py")

            # If we're in development mode, the version.py might not exist yet
            if not os.path.exists(version_py_path):
                # Create a minimal version.py for development mode
                from importlib.machinery import SourceFileLoader
                import yaml

                # Read directly from extension.yaml in development mode
                extension_file = os.path.join(base_dir, "extension.yaml")
                with open(extension_file, 'r') as file:
                    extension_data = yaml.safe_load(file)
                    version = extension_data.get('version', "unknown")

                # Return just the version without commit and build date
                return {"version": version}

            # Load the version module
            spec = importlib.util.spec_from_file_location("version", version_py_path)
            version_module = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(version_module)

            # Extract version information
            info["version"] = getattr(version_module, "VERSION", "unknown")
            info["commit"] = getattr(version_module, "COMMIT", None)
            info["build_date"] = getattr(version_module, "BUILD_DATE", None)

        except Exception as e:
            # Fall back to unknown version
            info["version"] = "unknown"

        return info