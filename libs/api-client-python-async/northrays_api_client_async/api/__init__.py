from __future__ import annotations

# flake8: noqa

# import apis into api package
import importlib
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from northrays_api_client_async.api.health_api import HealthApi
    from northrays_api_client_async.api.admin_api import AdminApi
    from northrays_api_client_async.api.api_keys_api import ApiKeysApi
    from northrays_api_client_async.api.audit_api import AuditApi
    from northrays_api_client_async.api.config_api import ConfigApi
    from northrays_api_client_async.api.docker_registry_api import DockerRegistryApi
    from northrays_api_client_async.api.jobs_api import JobsApi
    from northrays_api_client_async.api.object_storage_api import ObjectStorageApi
    from northrays_api_client_async.api.organizations_api import OrganizationsApi
    from northrays_api_client_async.api.preview_api import PreviewApi
    from northrays_api_client_async.api.regions_api import RegionsApi
    from northrays_api_client_async.api.runners_api import RunnersApi
    from northrays_api_client_async.api.sandbox_api import SandboxApi
    from northrays_api_client_async.api.snapshots_api import SnapshotsApi
    from northrays_api_client_async.api.toolbox_api import ToolboxApi
    from northrays_api_client_async.api.users_api import UsersApi
    from northrays_api_client_async.api.volumes_api import VolumesApi
    from northrays_api_client_async.api.webhooks_api import WebhooksApi


_DYNAMIC_IMPORTS: dict[str, str] = {
    "HealthApi": "northrays_api_client_async.api.health_api",
    "AdminApi": "northrays_api_client_async.api.admin_api",
    "ApiKeysApi": "northrays_api_client_async.api.api_keys_api",
    "AuditApi": "northrays_api_client_async.api.audit_api",
    "ConfigApi": "northrays_api_client_async.api.config_api",
    "DockerRegistryApi": "northrays_api_client_async.api.docker_registry_api",
    "JobsApi": "northrays_api_client_async.api.jobs_api",
    "ObjectStorageApi": "northrays_api_client_async.api.object_storage_api",
    "OrganizationsApi": "northrays_api_client_async.api.organizations_api",
    "PreviewApi": "northrays_api_client_async.api.preview_api",
    "RegionsApi": "northrays_api_client_async.api.regions_api",
    "RunnersApi": "northrays_api_client_async.api.runners_api",
    "SandboxApi": "northrays_api_client_async.api.sandbox_api",
    "SnapshotsApi": "northrays_api_client_async.api.snapshots_api",
    "ToolboxApi": "northrays_api_client_async.api.toolbox_api",
    "UsersApi": "northrays_api_client_async.api.users_api",
    "VolumesApi": "northrays_api_client_async.api.volumes_api",
    "WebhooksApi": "northrays_api_client_async.api.webhooks_api",

}


def __getattr__(attr_name: str) -> object:
    module_path = _DYNAMIC_IMPORTS.get(attr_name)
    if module_path is None:
        raise AttributeError(f"module {__name__!r} has no attribute {attr_name!r}")
    mod = importlib.import_module(module_path)
    value = getattr(mod, attr_name)
    globals()[attr_name] = value
    return value


def __dir__() -> list[str]:
    return list(_DYNAMIC_IMPORTS.keys())
