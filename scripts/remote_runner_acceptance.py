#!/usr/bin/env python3
"""Compatibility entry for the current Agent-driven acceptance, not managed execution.
Historical runner acceptance: scripts/history/remote_runner_acceptance_pre_agent_driven.py.
"""
from pathlib import Path
import runpy
runpy.run_path(str(Path(__file__).with_name("remote_agent_acceptance.py")), run_name="__main__")
