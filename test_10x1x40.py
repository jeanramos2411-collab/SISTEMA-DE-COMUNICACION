#!/usr/bin/env python3
"""
Prueba de PTT: 10 canales, 1 transmisor por canal, 40 receptores por canal
Total: 10 TX + 400 RX = 410 clientes simultáneos
"""

import asyncio
import json
import time
import random
from datetime import datetime
import websockets

WS_URL = "ws://localhost:8765"
CHANNELS = [f"LIBRE-{i}" for i in range(1, 11)]
TX_PER_CHANNEL = 1
RX_PER_CHANNEL = 40
TX_DURATION = 3  # segundos transmitiendo
RX_DURATION = 10  # segundos escuchando

class PttClient:
    def __init__(self, client_id, channel, is_transmitter=False):
        self.client_id = client_id
        self.channel = channel
        self.is_transmitter = is_transmitter
        self.ws = None
        self.connected = False
        self.joined = False
        self.transmitting = False
        self.received_audio = 0
        self.errors = []
        self.username = f"{'TX' if is_transmitter else 'RX'}-{channel}-{client_id}"

    async def connect(self):
        try:
            self.ws = await websockets.connect(
                WS_URL,
                max_size=2**20,
                ping_interval=None
            )
            self.connected = True
            return True
        except Exception as e:
            self.errors.append(f"Conexión: {e}")
            return False

    async def join(self):
        if not self.connected or not self.ws:
            return False
        try:
            await self.ws.send(json.dumps({
                "type": "join",
                "channel": self.channel,
                "username": self.username,
                "device_id": f"test-{self.client_id}"
            }))
            self.joined = True
            return True
        except Exception as e:
            self.errors.append(f"Join: {e}")
            return False

    async def receive_messages(self):
        """Recibe mensajes del servidor"""
        try:
            async for msg in self.ws:
                if isinstance(msg, bytes):
                    self.received_audio += len(msg)
                else:
                    data = json.loads(msg)
                    if data.get("type") == "ptt_started" and self.is_transmitter:
                        self.transmitting = True
                    elif data.get("type") == "ptt_ended" and self.is_transmitter:
                        self.transmitting = False
        except websockets.ConnectionClosed:
            pass
        except Exception as e:
            self.errors.append(f"Receive: {e}")

    async def transmit(self, duration=TX_DURATION):
        """Transmite audio PCM durante N segundos"""
        if not self.joined:
            return
        
        # Enviar ptt_start
        try:
            await self.ws.send(json.dumps({"type": "ptt_start"}))
            await asyncio.sleep(0.1)
            
            # Enviar audio (16000 Hz, 16-bit, mono = 32000 bytes/segundo)
            samples_per_sec = 16000 * 2  # 16-bit = 2 bytes
            chunk_size = 3200  # 100ms de audio
            chunks = int(duration * 10)
            
            silence = bytes(chunk_size)  # Silencio PCM
            
            for _ in range(chunks):
                await self.ws.send(silence)
                await asyncio.sleep(0.1)
            
            # Enviar ptt_end
            await self.ws.send(json.dumps({"type": "ptt_end"}))
            self.transmitting = False
        except Exception as e:
            self.errors.append(f"Transmit: {e}")

    async def listen(self, duration=RX_DURATION):
        """Escucha durante N segundos"""
        try:
            await asyncio.sleep(duration)
        except asyncio.CancelledError:
            pass

    async def run(self, action):
        """Ejecuta la acción (transmitir o escuchar)"""
        if action == "transmit":
            await self.transmit()
        else:
            await self.listen()

async def test_10x1x40():
    """Prueba: 10 canales × (1 TX + 40 RX)"""
    print(f"""
╔═══════════════════════════════════════════════════════════╗
║           PRUEBA: 10 CANALES × (1 TX + 40 RX)          ║
╠═══════════════════════════════════════════════════════════╣
║  Canales:     10 (LIBRE-1 a LIBRE-10)                   ║
║  TX/Canal:    1 transmisor                              ║
║  RX/Canal:    40 receptores                             ║
║  Total TX:    10 transmisores                           ║
║  Total RX:    400 receptores                            ║
║  Clientes:    410 total                                 ║
╚═══════════════════════════════════════════════════════════╝
""")
    
    clients = []
    start_time = time.time()
    
    # Crear todos los clientes
    print("[1/5] Creando clientes...")
    client_id = 0
    
    for channel in CHANNELS:
        # 1 transmisor por canal
        tx = PttClient(client_id, channel, is_transmitter=True)
        clients.append(tx)
        client_id += 1
        
        # 40 receptores por canal
        for _ in range(RX_PER_CHANNEL):
            rx = PttClient(client_id, channel, is_transmitter=False)
            clients.append(rx)
            client_id += 1
    
    print(f"      {len(clients)} clientes creados")
    
    # Conectar todos
    print("[2/5] Conectando clientes...")
    connect_tasks = [c.connect() for c in clients]
    results = await asyncio.gather(*connect_tasks)
    connected = sum(1 for r in results if r)
    print(f"      {connected}/{len(clients)} conectados")
    
    if connected < len(clients) * 0.9:
        print(f"❌ Muchos clientes no pudieron conectarse: {connected}/{len(clients)}")
        return
    
    # Hacer join a los canales
    print("[3/5] Uniendo clientes a canales...")
    join_tasks = [c.join() for c in clients]
    results = await asyncio.gather(*join_tasks)
    joined = sum(1 for r in results if r)
    print(f"      {joined}/{len(clients)} unidos")
    
    await asyncio.sleep(1)  # Esperar a que todos estén listos
    
    # Iniciar transmisión - cada TX transmite en su canal
    print("[4/5] Ejecutando prueba de transmisión...")
    print("      Cada transmisor envía audio durante 3 segundos")
    
    # Iniciar todas las tareas de recepción primero
    rx_tasks = []
    for c in clients:
        if not c.is_transmitter:
            task = asyncio.create_task(c.receive_messages())
            rx_tasks.append(task)
    
    # Iniciar transmisión de cada TX
    tx_tasks = []
    for c in clients:
        if c.is_transmitter:
            task = asyncio.create_task(c.run("transmit"))
            tx_tasks.append(task)
    
    # Esperar a que todas las transmisiones terminen
    await asyncio.gather(*tx_tasks)
    print("      Transmisiones completadas")
    
    # Esperar un poco más para que los RX reciban todo
    await asyncio.sleep(2)
    
    # Cancelar las tareas de recepción
    for task in rx_tasks:
        task.cancel()
    await asyncio.gather(*rx_tasks, return_exceptions=True)
    
    end_time = time.time()
    duration = end_time - start_time
    
    # Resultados
    print("[5/5] Analizando resultados...")
    
    print(f"""
╔═══════════════════════════════════════════════════════════╗
║                    RESULTADOS DE LA PRUEBA               ║
╠═══════════════════════════════════════════════════════════╣""")
    
    print(f"║  Duración total:    {duration:.1f} segundos                          ║")
    print(f"║  Clientes creados: {len(clients)}                                   ║")
    print(f"║  Conectados:       {connected}                                      ║")
    print(f"║  Unidos:            {joined}                                          ║")
    
    # Estadísticas de TX
    tx_clients = [c for c in clients if c.is_transmitter]
    rx_clients = [c for c in clients if not c.is_transmitter]
    
    tx_with_errors = sum(1 for c in tx_clients if c.errors)
    rx_with_audio = sum(1 for c in rx_clients if c.received_audio > 0)
    total_audio_rx = sum(c.received_audio for c in rx_clients)
    
    print(f"║                                                           ║")
    print(f"║  TRANSMISORES (10):                                       ║")
    print(f"║    - Con errores:    {tx_with_errors}                                     ║")
    
    print(f"║                                                           ║")
    print(f"║  RECEPTORES (400):                                       ║")
    print(f"║    - Recibieron audio: {rx_with_audio}                              ║")
    print(f"║    - Bytes recibidos: {total_audio_rx:,}                          ║")
    
    # Verificar que los RX en cada canal recibieron audio
    print(f"║                                                           ║")
    print(f"║  AUDIO POR CANAL:                                        ║")
    
    for channel in CHANNELS:
        rx_in_channel = [c for c in rx_clients if c.channel == channel]
        with_audio = sum(1 for c in rx_in_channel if c.received_audio > 0)
        total_in_channel = len(rx_in_channel)
        percentage = (with_audio / total_in_channel * 100) if total_in_channel > 0 else 0
        status = "✓" if percentage >= 90 else "✗" if percentage >= 50 else "!"
        print(f"║    {status} {channel}: {with_audio}/{total_in_channel} RX ({percentage:.0f}%)                  ║")
    
    print(f"╚═══════════════════════════════════════════════════════════╝""")
    
    # Guardar resultados
    results = {
        "test": "10x1x40",
        "timestamp": datetime.now().isoformat(),
        "duration_seconds": duration,
        "total_clients": len(clients),
        "connected": connected,
        "joined": joined,
        "transmitters": {
            "count": len(tx_clients),
            "with_errors": tx_with_errors,
            "errors": [e for c in tx_clients for e in c.errors]
        },
        "receivers": {
            "count": len(rx_clients),
            "received_audio": rx_with_audio,
            "total_bytes": total_audio_rx
        },
        "per_channel": {
            channel: {
                "rx_with_audio": sum(1 for c in rx_clients if c.channel == channel and c.received_audio > 0),
                "total_rx": sum(1 for c in rx_clients if c.channel == channel)
            }
            for channel in CHANNELS
        }
    }
    
    filename = f"test_results_10x1x40_python.json"
    with open(filename, "w") as f:
        json.dump(results, f, indent=2)
    print(f"\n📄 Resultados guardados: {filename}")
    
    # Veredicto
    success_rate = rx_with_audio / len(rx_clients) * 100 if rx_clients else 0
    if success_rate >= 95:
        print(f"\n✅ PRUEBA EXITOSA: {success_rate:.1f}% de receptores recibió audio")
    elif success_rate >= 80:
        print(f"\n⚠️ PRUEBA ACEPTABLE: {success_rate:.1f}% de receptores recibió audio")
    else:
        print(f"\n❌ PRUEBA FALLIDA: Solo {success_rate:.1f}% de receptores recibió audio")

if __name__ == "__main__":
    asyncio.run(test_10x1x40())
