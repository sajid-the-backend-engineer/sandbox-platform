from __future__ import annotations

# flake8: noqa

# import apis into api package
import importlib
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from northrays_toolbox_api_client.api.computer_use_api import ComputerUseApi
    from northrays_toolbox_api_client.api.file_system_api import FileSystemApi
    from northrays_toolbox_api_client.api.git_api import GitApi
    from northrays_toolbox_api_client.api.info_api import InfoApi
    from northrays_toolbox_api_client.api.interpreter_api import InterpreterApi
    from northrays_toolbox_api_client.api.lsp_api import LspApi
    from northrays_toolbox_api_client.api.port_api import PortApi
    from northrays_toolbox_api_client.api.process_api import ProcessApi
    from northrays_toolbox_api_client.api.server_api import ServerApi


_DYNAMIC_IMPORTS: dict[str, str] = {
    "ComputerUseApi": "northrays_toolbox_api_client.api.computer_use_api",
    "FileSystemApi": "northrays_toolbox_api_client.api.file_system_api",
    "GitApi": "northrays_toolbox_api_client.api.git_api",
    "InfoApi": "northrays_toolbox_api_client.api.info_api",
    "InterpreterApi": "northrays_toolbox_api_client.api.interpreter_api",
    "LspApi": "northrays_toolbox_api_client.api.lsp_api",
    "PortApi": "northrays_toolbox_api_client.api.port_api",
    "ProcessApi": "northrays_toolbox_api_client.api.process_api",
    "ServerApi": "northrays_toolbox_api_client.api.server_api",

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
