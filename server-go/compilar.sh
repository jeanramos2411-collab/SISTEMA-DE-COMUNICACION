#!/bin/bash

# Script para compilar el servidor PTT en Go

echo "Compilando servidor PTT..."

cd "$(dirname "$0")"

# Limpiar builds anteriores
rm -f ptt-server

# Compilar para Linux
go build -o ptt-server ./cmd/ptt-server

if [ $? -eq 0 ]; then
    echo ""
    echo "✓ Compilación exitosa!"
    echo ""
    echo "Para ejecutar el servidor:"
    echo "  ./ptt-server"
    echo ""
    echo "Panel admin: http://localhost:8766/admin"
    echo "Clave admin: admin123"
else
    echo ""
    echo "✗ Error en la compilación."
    echo "Asegúrate de tener Go instalado."
    exit 1
fi
