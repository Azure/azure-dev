"""
Unit tests for eval graders.

Tests grade() functions with mocked HTTP responses to verify scoring logic
without making real Azure API or HTTP calls.
"""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

import json
from unittest.mock import patch, MagicMock
from urllib.error import HTTPError, URLError

import app_health
import cleanup_validator
import infra_validator


# ---------------------------------------------------------------------------
# app_health tests
# ---------------------------------------------------------------------------

class TestAppHealthGrader:
    """Tests for app_health.grade()."""

    def test_no_endpoints_returns_zero(self):
        result = app_health.grade({"params": {}})
        assert result["score"] == 0.0
        assert "No endpoints" in result["reason"]

    @patch("app_health.urlopen")
    def test_all_endpoints_healthy(self, mock_urlopen):
        resp = MagicMock()
        resp.status = 200
        resp.read.return_value = b'{"status": "healthy"}'
        mock_urlopen.return_value = resp

        result = app_health.grade({
            "params": {
                "endpoints": [
                    {"url": "https://example.com/", "expected_status": 200},
                    {"url": "https://example.com/api", "expected_status": 200},
                ],
                "retries": 1,
            }
        })
        assert result["score"] == 1.0
        assert "All 2 health checks passed" in result["reason"]

    @patch("app_health.urlopen")
    def test_partial_failure(self, mock_urlopen):
        success_resp = MagicMock()
        success_resp.status = 200
        success_resp.read.return_value = b"OK"

        fail_resp = MagicMock()
        fail_resp.status = 500
        fail_resp.read.return_value = b"error"

        mock_urlopen.side_effect = [success_resp, fail_resp]

        result = app_health.grade({
            "params": {
                "endpoints": [
                    {"url": "https://example.com/ok", "expected_status": 200},
                    {"url": "https://example.com/fail", "expected_status": 200},
                ],
                "retries": 1,
            }
        })
        assert result["score"] == 0.5

    @patch("app_health.urlopen")
    def test_body_content_mismatch_retries(self, mock_urlopen):
        """Body-content mismatches should retry (not fail immediately)."""
        bad_resp = MagicMock()
        bad_resp.status = 200
        bad_resp.read.return_value = b"loading..."

        good_resp = MagicMock()
        good_resp.status = 200
        good_resp.read.return_value = b'{"status": "healthy"}'

        mock_urlopen.side_effect = [bad_resp, good_resp]

        result = app_health.grade({
            "params": {
                "endpoints": [
                    {
                        "url": "https://example.com/",
                        "expected_status": 200,
                        "expected_body_contains": "healthy",
                    },
                ],
                "retries": 2,
                "retry_delay": 0,
            }
        })
        assert result["score"] == 1.0
        assert mock_urlopen.call_count == 2

    @patch("app_health.urlopen")
    def test_connection_error(self, mock_urlopen):
        mock_urlopen.side_effect = URLError("Connection refused")

        result = app_health.grade({
            "params": {
                "endpoints": [{"url": "https://down.example.com/"}],
                "retries": 1,
            }
        })
        assert result["score"] == 0.0
        assert "Connection error" in result["reason"]

    @patch("app_health.urlopen")
    def test_non_2xx_expected_status(self, mock_urlopen):
        """Non-2xx expected_status should match against HTTPError code."""
        mock_urlopen.side_effect = HTTPError(
            url="", code=404, msg="Not Found", hdrs={}, fp=None
        )

        result = app_health.grade({
            "params": {
                "endpoints": [
                    {"url": "https://example.com/deleted", "expected_status": 404},
                ],
                "retries": 1,
            }
        })
        assert result["score"] == 1.0

    @patch("app_health.urlopen")
    def test_non_2xx_unexpected_status(self, mock_urlopen):
        """HTTPError with wrong code should fail."""
        mock_urlopen.side_effect = HTTPError(
            url="", code=500, msg="Server Error", hdrs={}, fp=None
        )

        result = app_health.grade({
            "params": {
                "endpoints": [
                    {"url": "https://example.com/fail", "expected_status": 200},
                ],
                "retries": 1,
            }
        })
        assert result["score"] == 0.0

    def test_empty_url_fails(self):
        result = app_health.grade({
            "params": {
                "endpoints": [{"url": ""}],
            }
        })
        assert result["score"] == 0.0
        assert "Empty URL" in result["reason"]


# ---------------------------------------------------------------------------
# cleanup_validator tests
# ---------------------------------------------------------------------------

class TestCleanupValidatorGrader:
    """Tests for cleanup_validator.grade()."""

    def test_missing_params_returns_zero(self):
        result = cleanup_validator.grade({"params": {}})
        assert result["score"] == 0.0
        assert "Missing" in result["reason"]

    @patch("cleanup_validator.get_access_token")
    @patch("cleanup_validator.urlopen")
    def test_resource_group_deleted(self, mock_urlopen, mock_token):
        mock_token.return_value = "fake-token"
        mock_urlopen.side_effect = HTTPError(
            url="", code=404, msg="Not Found", hdrs={}, fp=None
        )

        result = cleanup_validator.grade({
            "params": {
                "subscription_id": "sub-123",
                "resource_group": "rg-deleted",
            }
        })
        assert result["score"] == 1.0
        assert "successfully deleted" in result["reason"]

    @patch("cleanup_validator.get_access_token")
    @patch("cleanup_validator.urlopen")
    def test_resource_group_still_exists(self, mock_urlopen, mock_token):
        mock_token.return_value = "fake-token"

        rg_resp = MagicMock()
        rg_resp.read.return_value = json.dumps({
            "properties": {"provisioningState": "Succeeded"}
        }).encode()

        resources_resp = MagicMock()
        resources_resp.read.return_value = json.dumps({
            "value": [{"name": "mysite", "type": "Microsoft.Web/sites"}]
        }).encode()

        mock_urlopen.side_effect = [rg_resp, resources_resp]

        result = cleanup_validator.grade({
            "params": {
                "subscription_id": "sub-123",
                "resource_group": "rg-exists",
            }
        })
        assert result["score"] == 0.0
        assert "still exists" in result["reason"]

    @patch("cleanup_validator.get_access_token")
    @patch("cleanup_validator.urlopen")
    def test_resource_group_deleting(self, mock_urlopen, mock_token):
        mock_token.return_value = "fake-token"

        resp = MagicMock()
        resp.read.return_value = json.dumps({
            "properties": {"provisioningState": "Deleting"}
        }).encode()
        mock_urlopen.return_value = resp

        result = cleanup_validator.grade({
            "params": {
                "subscription_id": "sub-123",
                "resource_group": "rg-deleting",
            }
        })
        assert result["score"] == 0.5
        assert "Deleting" in result["reason"]


# ---------------------------------------------------------------------------
# infra_validator tests
# ---------------------------------------------------------------------------

class TestInfraValidatorGrader:
    """Tests for infra_validator.grade()."""

    def test_missing_params_returns_zero(self):
        result = infra_validator.grade({"params": {}})
        assert result["score"] == 0.0
        assert "Missing" in result["reason"]

    @patch("infra_validator.get_access_token")
    @patch("infra_validator.urlopen")
    def test_resource_group_not_found(self, mock_urlopen, mock_token):
        mock_token.return_value = "fake-token"
        mock_urlopen.side_effect = HTTPError(
            url="", code=404, msg="Not Found", hdrs={}, fp=None
        )

        result = infra_validator.grade({
            "params": {
                "subscription_id": "sub-123",
                "resource_group": "rg-missing",
            }
        })
        assert result["score"] == 0.0
        assert "does not exist" in result["reason"]

    @patch("infra_validator.get_access_token")
    @patch("infra_validator.urlopen")
    def test_all_expected_resources_found(self, mock_urlopen, mock_token):
        mock_token.return_value = "fake-token"

        rg_resp = MagicMock()
        rg_resp.read.return_value = b'{"properties": {"provisioningState": "Succeeded"}}'

        resources_resp = MagicMock()
        resources_resp.read.return_value = json.dumps({
            "value": [
                {"type": "Microsoft.Web/sites", "name": "mysite"},
                {"type": "Microsoft.DocumentDB/databaseAccounts", "name": "mydb"},
            ]
        }).encode()

        mock_urlopen.side_effect = [rg_resp, resources_resp]

        result = infra_validator.grade({
            "params": {
                "subscription_id": "sub-123",
                "resource_group": "rg-test",
                "expected_resources": [
                    "Microsoft.Web/sites",
                    "Microsoft.DocumentDB/databaseAccounts",
                ],
            }
        })
        assert result["score"] == 1.0
        assert "All expected resources found" in result["reason"]

    @patch("infra_validator.get_access_token")
    @patch("infra_validator.urlopen")
    def test_missing_expected_resources(self, mock_urlopen, mock_token):
        mock_token.return_value = "fake-token"

        rg_resp = MagicMock()
        rg_resp.read.return_value = b'{"properties": {"provisioningState": "Succeeded"}}'

        resources_resp = MagicMock()
        resources_resp.read.return_value = json.dumps({
            "value": [{"type": "Microsoft.Web/sites", "name": "mysite"}]
        }).encode()

        mock_urlopen.side_effect = [rg_resp, resources_resp]

        result = infra_validator.grade({
            "params": {
                "subscription_id": "sub-123",
                "resource_group": "rg-test",
                "expected_resources": [
                    "Microsoft.Web/sites",
                    "Microsoft.DocumentDB/databaseAccounts",
                ],
            }
        })
        assert result["score"] == 0.5
        assert "Missing resources" in result["reason"]

    @patch("infra_validator.get_access_token")
    @patch("infra_validator.urlopen")
    def test_rg_exists_no_expected_resources(self, mock_urlopen, mock_token):
        mock_token.return_value = "fake-token"

        resp = MagicMock()
        resp.read.return_value = b'{"properties": {"provisioningState": "Succeeded"}}'
        mock_urlopen.return_value = resp

        result = infra_validator.grade({
            "params": {
                "subscription_id": "sub-123",
                "resource_group": "rg-test",
            }
        })
        assert result["score"] == 1.0
        assert "exists" in result["reason"]
