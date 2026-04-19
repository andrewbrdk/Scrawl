# Scrawl
Personal Notes App

The service starts at http://localhost:8080/ after the following commands:
```bash
git clone https://github.com/andrewbrdk/Scrawl
cd ./Scrawl
npm ci
npm run build
go get scrawl
go build
./scrawl
```
  
Env. variables
```bash
SCRAWL_PASSWORD              # Password for the web UI
SCRAWL_PORT                  # HTTP port (default: `8080`)
SCRAWL_DBFILE                # Sqlite database file  
```