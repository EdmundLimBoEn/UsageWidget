"""Exercise installer rollback with real SQLite files and the production migration."""
import os
from pathlib import Path
import re
import sqlite3
import sys

mode, db_path, root = sys.argv[1:]
source = (Path(root) / "server/store.go").read_text()
db = sqlite3.connect(db_path)
legacy_upsert = """
INSERT INTO alert_delivery_state(window_id, reset_epoch, policy_fingerprint, last_delivered_at)
VALUES('window', 'epoch', 'policy', ?)
ON CONFLICT(window_id, reset_epoch, policy_fingerprint)
DO UPDATE SET last_delivered_at=excluded.last_delivered_at
"""
if mode == "init":
    db.executescript(re.search(r'const schema = `(.*?)`', source, re.S)[1])
    db.execute("INSERT INTO schema_migrations VALUES(1, 'before')")
    db.execute("INSERT INTO devices VALUES('device', '', '', 'before')")
    db.commit()
    db.execute("PRAGMA journal_mode=WAL")
    db.execute(legacy_upsert, ("before",))
    db.commit()
    # Leave committed cadence data only in WAL, as with an unclean daemon exit.
    os._exit(0)
elif mode == "migrate":
    migration = re.search(r'if version < 2 \{\s*_, err := tx.Exec\(`(.*?)`\)', source, re.S)[1]
    db.executescript(migration)
    db.execute("INSERT INTO schema_migrations VALUES(2, 'after')")
    db.execute("UPDATE alert_delivery_state SET last_delivered_at='after'")
    db.commit()
    db.execute("PRAGMA journal_mode=WAL")
    db.execute("UPDATE alert_delivery_state SET last_delivered_at='failed-release'")
    db.commit()
    os._exit(0)
elif mode == "old":
    assert db.execute("PRAGMA integrity_check").fetchone()[0] == "ok"
    assert db.execute("SELECT MAX(version) FROM schema_migrations").fetchone()[0] == 1
    assert db.execute("SELECT last_delivered_at FROM alert_delivery_state").fetchone()[0] == "before"
    # This is the previous binary's conflict target: it fails against schema v2.
    db.execute(legacy_upsert, ("rolled-back",))
    db.commit()
elif mode == "new":
    assert db.execute("SELECT MAX(version) FROM schema_migrations").fetchone()[0] == 2
else:
    raise ValueError(mode)
db.close()
