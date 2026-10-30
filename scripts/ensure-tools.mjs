#!/usr/bin/env node
import { ensureGo, ensureNode } from "./toolchain.mjs";

ensureNode();
await ensureGo();
