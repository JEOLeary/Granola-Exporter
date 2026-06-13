const path = require('path');
const fs = require('fs');

const dbPath = process.argv[2];
const hexKey = process.argv[3];
const modulePath = process.argv[4] ||
    'C:\\Users\\JEOLeary\\AppData\\Local\\Programs\\granola\\resources\\app.asar.unpacked\\node_modules\\better-sqlite3-multiple-ciphers';

function die(msg) {
    console.error(JSON.stringify({ error: msg }));
    process.exit(1);
}

if (!dbPath) die('Missing dbPath argument');
if (!hexKey) die('Missing hexKey argument');

const binding = path.join(modulePath, 'build', 'Release', 'better_sqlite3.node');
if (!fs.existsSync(binding)) die('Native binding not found: ' + binding);

let Database;
try {
    Database = require(path.join(modulePath, 'lib', 'index.js'));
} catch (e) {
    die('Failed to load module: ' + e.message);
}

let db;
try {
    db = new Database(dbPath, { readonly: true, nativeBinding: binding });
    db.pragma('cipher=sqlcipher');
    db.pragma('legacy=4');
    db.key(Buffer.from(hexKey, 'hex'));
} catch (e) {
    die('Failed to open database: ' + e.message);
}

try {
    const tables = db.prepare("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name").all();
    const documents = [];

    for (const t of tables) {
        try {
            const cols = db.prepare(`PRAGMA table_info("${t.name}")`).all();
            const colNames = cols.map(c => c.name);
            const rows = db.prepare(`SELECT * FROM "${t.name}"`).all();
            for (const row of rows) {
                documents.push({ table: t.name, columns: colNames, row });
            }
        } catch (e) {
            if (!e.message.includes('no such table')) {
                documents.push({ table: t.name, error: e.message });
            }
        }
    }

    console.log(JSON.stringify({
        success: true,
        tables: tables.map(t => t.name),
        documents,
        dbPath
    }));
} catch (e) {
    die('Query error: ' + e.message);
} finally {
    if (db) db.close();
}
