# notes – Demo-Anwendung für „Shell-Scripting zum Anfassen"

notes-api (Go) speichert Notizen in Postgres. Im Buch steht nginx davor
(`../notes-stack`) und schreibt ein Zugriffslog, das die Skripte auswerten.

- `POST /notes` legt eine Notiz an und verlangt den Header `X-API-Key`
  (Laborwert `lab-api-key-123`).
- `GET /notes` listet alle Notizen als JSON, `GET /healthz` antwortet `ok`,
  `GET /version` mit der Version, `GET /metrics` mit Kennzahlen im Prometheus-Format.
- `notes-api migrate` legt die Tabelle an (idempotent).

Bauen (Tag 0): `docker build --build-arg VERSION=0.4.0 -t notes-api:0.4.0 notes-api`
