import sys


def get_text():
    version = sys.version_info
    return f"Hello World, I am Python {version.major}.{version.minor}"