#!/usr/bin/env python3
"""
Script de prueba que simula la conexión del cliente Android.
Usa la misma lógica que el APK: OkHttp-style WebSocket connection.
"""

import asyncio
import json
import websockets
import time

SERVER_IP = "localhost"
SERVER_PORT = 8765
CHANNEL = "CANAL LIBRE"
USERNAME = "TestAndroidUser"
DEVICE_ID = "test-android-device"
MAC = "AA:BB:CC:DD:EE:FF"

async def test_android_connection():
    url = f"ws://{SERVER_IP}:{SERVER_PORT}"
    print(f"[TEST] Conectando a {url}...")
    
    try:
        # Crear conexión WebSocket igual que OkHttp
        ws = await websockets.connect(
            url,
            ping_interval=20,  # Igual que Android: pingInterval(20, TimeUnit.SECONDS)
            ping_timeout=60,
            max_size=1024*1024,  # 1MB igual que Go server
            close_timeout=10
        )
        print("[TEST] ✓ Conexión WebSocket establecida")
        
        # Enviar mensaje de join igual que Android
        join_msg = {
            "type": "join",
            "channel": CHANNEL,
            "username": USERNAME,
            "device_id": DEVICE_ID,
            "mac": MAC
        }
        print(f"[TEST] Enviando join: {join_msg}")
        await ws.send(json.dumps(join_msg))
        print("[TEST] ✓ Mensaje join enviado")
        
        # Esperar respuesta con timeout
        print("[TEST] Esperando respuesta del servidor...")
        response_tasks = [
            asyncio.create_task(ws.recv()),
            asyncio.create_task(asyncio.sleep(10))
        ]
        done, pending = await asyncio.wait(
            response_tasks, 
            return_when=asyncio.FIRST_COMPLETED
        )
        
        for task in pending:
            task.cancel()
        
        for task in done:
            result = task.result()
            if isinstance(result, str):
                print(f"[TEST] ✓ Respuesta recibida: {result}")
                data = json.loads(result)
                if data.get("type") == "joined":
                    print("[TEST] ✓ JOIN EXITOSO - El cliente Android debería funcionar")
                    print(f"[TEST]   Channel: {data.get('channel')}")
                    print(f"[TEST]   Users: {data.get('users')}")
                elif data.get("type") == "error":
                    print(f"[TEST] ✗ ERROR del servidor: {data.get('message')}")
                else:
                    print(f"[TEST] ? Tipo de mensaje desconocido: {data.get('type')}")
            else:
                print(f"[TEST] ? Mensaje binario recibido (esto sería un problema): {type(result)}")
        
        # Mantener conexión un poco más para ver si llegan más mensajes
        print("[TEST] Manteniendo conexión por 5 segundos...")
        await asyncio.sleep(5)
        
        await ws.close()
        print("[TEST] Conexión cerrada")
        
    except websockets.exceptions.WebSocketException as e:
        print(f"[TEST] ✗ Error de WebSocket: {e}")
    except Exception as e:
        print(f"[TEST] ✗ Error: {type(e).__name__}: {e}")

async def test_ping_pong():
    """Prueba el mecanismo de ping/pong"""
    url = f"ws://{SERVER_IP}:{SERVER_PORT}"
    print(f"\n[TEST PING] Conectando a {url}...")
    
    try:
        ws = await websockets.connect(url, ping_interval=20, ping_timeout=60)
        print("[TEST PING] ✓ Conectado")
        
        # Enviar ping manualmente como hace el cliente Android
        await ws.send(json.dumps({"type": "ping"}))
        print("[TEST PING] ✓ Ping enviado")
        
        # Esperar pong
        response = await asyncio.wait_for(ws.recv(), timeout=5)
        data = json.loads(response)
        if data.get("type") == "pong":
            print("[TEST PING] ✓ Pong recibido")
        else:
            print(f"[TEST PING] ? Respuesta: {data}")
        
        await ws.close()
        
    except asyncio.TimeoutError:
        print("[TEST PING] ✗ Timeout esperando pong")
    except Exception as e:
        print(f"[TEST PING] ✗ Error: {e}")

async def main():
    print("=" * 60)
    print("PRUEBA DE CONEXIÓN TIPO CLIENTE ANDROID")
    print("=" * 60)
    
    await test_android_connection()
    await test_ping_pong()

if __name__ == "__main__":
    asyncio.run(main())
