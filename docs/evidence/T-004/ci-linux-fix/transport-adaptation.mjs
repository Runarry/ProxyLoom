import { readFileSync, writeFileSync } from 'node:fs';
const path = 'scripts/verify-foundation.mjs';
let source = readFileSync(path, 'utf8');
source = source.replace('owned.network = true;', "owned.network = true;\n  docker(['network', 'connect', name, process.env.REPRO_CLIENT], 'connect_repro_client');");
source = source.replaceAll('@127.0.0.1:${port}/proxyloom', '@${name}:5432/proxyloom');
source = source.replace('} finally {\n', "} finally {\n  if (owned.network) docker(['network', 'disconnect', name, process.env.REPRO_CLIENT], 'disconnect_repro_client');\n");
source = source.replace('} finally {\r\n', "} finally {\n  if (owned.network) docker(['network', 'disconnect', name, process.env.REPRO_CLIENT], 'disconnect_repro_client');\n");
writeFileSync(path, source);
