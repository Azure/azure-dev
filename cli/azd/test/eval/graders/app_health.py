"""
Waza code grader: Validates a deployed application is healthy after azd deploy.

Makes HTTP requests to specified endpoints and checks status codes and optional
response body content. Returns a proportional score based on how many health
checks pass.

Usage in task YAML:
  graders:
    - type: code
      config:
        language: python
        file: graders/app_health.py
        params:
          endpoints:
            - url: "https://myapp.azurewebsites.net/"
              expected_status: 200
            - url: "https://myapp.azurewebsites.net/api/health"
              expected_status: 200
              expected_body_contains: "healthy"
            - url: "https://myapp.azurewebsites.net/api/version"
              expected_status: 200
              expected_body_contains: "version"
          timeout: 30
          retries: 3
          retry_delay: 5
"""
import time
from urllib.request import Request, urlopen
from urllib.error import HTTPError, URLError


def check_endpoint(
    url: str,
    expected_status: int = 200,
    expected_body_contains: str = "",
    timeout: int = 30,
    retries: int = 3,
    retry_delay: int = 5,
) -> dict:
    """Check a single endpoint, with retries for transient failures."""
    last_error = None

    for attempt in range(retries):
        try:
            req = Request(url, method="GET")
            resp = urlopen(req, timeout=timeout)
            status = resp.status
            body = resp.read().decode("utf-8", errors="replace")

            if status != expected_status:
                last_error = f"Expected status {expected_status}, got {status}"
                continue

            if expected_body_contains and expected_body_contains not in body:
                last_error = f"Response body missing expected string '{expected_body_contains}'"
                continue

            return {"passed": True, "reason": f"Status {status} OK"}

        except HTTPError as e:
            last_error = f"HTTP {e.code}: {e.reason}"
        except URLError as e:
            last_error = f"Connection error: {e.reason}"
        except TimeoutError:
            last_error = f"Request timed out after {timeout}s"
        except Exception as e:
            last_error = f"Unexpected error: {e}"

        if attempt < retries - 1:
            time.sleep(retry_delay)

    return {"passed": False, "reason": last_error or "Unknown error"}


def grade(context: dict) -> dict:
    """Waza grader entry point."""
    params = context.get("params", {})
    endpoints = params.get("endpoints", [])
    default_timeout = params.get("timeout", 30)
    default_retries = params.get("retries", 3)
    default_retry_delay = params.get("retry_delay", 5)

    if not endpoints:
        return {"score": 0.0, "reason": "No endpoints specified in params"}

    results = []
    for ep in endpoints:
        url = ep.get("url", "")
        if not url:
            results.append({"url": "", "passed": False, "reason": "Empty URL"})
            continue

        result = check_endpoint(
            url=url,
            expected_status=ep.get("expected_status", 200),
            expected_body_contains=ep.get("expected_body_contains", ""),
            timeout=ep.get("timeout", default_timeout),
            retries=ep.get("retries", default_retries),
            retry_delay=ep.get("retry_delay", default_retry_delay),
        )
        result["url"] = url
        results.append(result)

    passed = sum(1 for r in results if r["passed"])
    score = passed / len(results)

    failed = [r for r in results if not r["passed"]]
    if failed:
        details = "; ".join(f"{r['url']}: {r['reason']}" for r in failed)
        return {"score": score, "reason": f"Failed checks: {details}"}

    return {"score": 1.0, "reason": f"All {len(results)} health checks passed"}
