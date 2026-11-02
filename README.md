# OpenTofu zum Anfassen – Begleitcode

Begleitcode zum Buch **OpenTofu zum Anfassen** – Infrastruktur als Code, kompatibel mit Terraform. Teil der Reihe IT-Trainingslager von Thomas Zachmann.

Buchseite: https://it-trainingslager.de/buecher/opentofu

Der Code begleitet das Buch; Erklärungen und Übungen stehen im Buch. Die OpenTofu-Konfigurationen schreibst du im Buch selbst – hier liegt nur, was du dafür brauchst. Alle Schlüssel und Passwörter sind Laborwerte – nie in Produktion verwenden.

## Inhalt

- `setup/` – `env.sh` mit `LAB`, `VM_IP`, `KUBECONFIG`, Provider-Cache und `NOTES_API_KEY`
- `notes/` – Quellcode von notes-api (Go), der Anwendung, die OpenTofu ausrollt
- `charts/notes/` – das Helm-Chart für notes (Tag 7)

## Schnellstart

```bash
git clone https://github.com/thomaszachmann/opentofu-zum-anfassen.git ~/tf-lab/code
```

Details stehen im Kapitel „Tag 0“ des Buchs.

## Lizenz

Code: MIT (siehe LICENSE). Buchtexte, Abbildungen und Cover sind nicht Teil dieses Repos und nicht lizenziert.
