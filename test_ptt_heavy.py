#!/usr/bin/env python3
"""
Prueba exhaustiva de carga PTT con:
- 10 canales simultáneos
- 10 transmisores (1 por canal)
- 40 receptores por canal (400 receptores totales)
- Total: 410 clientes simultáneos
"""

import asyncio
import json
import random
import time
import websockets
from dataclasses import dataclass, field
from typing import List, Optional, Dict
from datetime import datetime


@dataclass
class PTTClient:
    id: int
    username: str
    ws: Optional[websockets.WebSocketClientProtocol] = None
    connected: bool = False
    joined: bool = False
    channel: Optional[str] = None
    is_transmitter: bool = False
    session_id: Optional[str] = None
    audio_chunks_sent: int = 0
    audio_chunks_received: int = 0
    errors: List[str] = field(default_factory=list)


class HeavyLoadTester:
    def __init__(self, server_url: str, num_channels: int = 10, 
                 transmitters_per_channel: int = 1, 
                 receivers_per_channel: int = 40):
        self.server_url = server_url
        self.num_channels = num_channels
        self.transmitters_per_channel = transmitters_per_channel
        self.receivers_per_channel = receivers_per_channel
        
        self.total_clients = num_channels * (transmitters_per_channel + receivers_per_channel)
        self.clients: List[PTTClient] = []
        self.transmitters: List[PTTClient] = []
        self.receivers: Dict[str, List[PTTClient]] = {}  # channel -> receivers
        
        # Usar canales disponibles del servidor
        self.channels = [f"CANAL-{i+1}" for i in range(num_channels)]
        
        self.results = {
            "server_url": server_url,
            "num_channels": num_channels,
            "transmitters_per_channel": transmitters_per_channel,
            "receivers_per_channel": receivers_per_channel,
            "total_clients": self.total_clients,
            "start_time": None,
            "end_time": None,
            "duration_seconds": 0,
            "connection_stats": {"total": 0, "successful": 0, "failed": 0},
            "join_stats": {"total": 0, "successful": 0, "failed": 0},
            "transmission_stats": {"total": 0, "successful": 0, "denied": 0},
            "audio_stats": {"chunks_sent": 0, "chunks_received": 0},
            "channel_stats": {},
            "success": False,
            "errors": [],
        }

    def generate_audio_chunk(self, size: int = 640) -> bytes:
        return bytes(random.getrandbits(8) for _ in range(size))

    async def connect_client(self, client: PTTClient) -> bool:
        self.results["connection_stats"]["total"] += 1
        try:
            client.ws = await websockets.connect(
                self.server_url,
                max_size=10 * 1024 * 1024,
                ping_interval=20,
                ping_timeout=60
            )
            client.connected = True
            self.results["connection_stats"]["successful"] += 1
            return True
        except Exception as e:
            client.errors.append(f"Conexión: {str(e)}")
            self.results["connection_stats"]["failed"] += 1
            return False

    async def join_channel(self, client: PTTClient, channel: str) -> bool:
        if not client.connected or not client.ws:
            return False

        self.results["join_stats"]["total"] += 1
        try:
            join_msg = {
                "type": "join",
                "username": client.username,
                "channel": channel,
                "device_id": f"device-{client.id}",
            }
            await client.ws.send(json.dumps(join_msg))
            
            response = await asyncio.wait_for(client.ws.recv(), timeout=10.0)
            data = json.loads(response)
            
            if data.get("type") == "joined":
                client.joined = True
                client.channel = channel
                self.results["join_stats"]["successful"] += 1
                return True
            else:
                self.results["join_stats"]["failed"] += 1
                return False
        except asyncio.TimeoutError:
            self.results["join_stats"]["failed"] += 1
            return False
        except Exception as e:
            self.results["join_stats"]["failed"] += 1
            return False

    async def receive_messages(self, client: PTTClient, stop_event: asyncio.Event):
        try:
            while client.connected and client.ws and not stop_event.is_set():
                try:
                    msg = await asyncio.wait_for(client.ws.recv(), timeout=0.5)
                    
                    if isinstance(msg, bytes):
                        client.audio_chunks_received += 1
                        self.results["audio_stats"]["chunks_received"] += 1
                    else:
                        data = json.loads(msg)
                        msg_type = data.get("type")
                        if msg_type in ("ptt_started", "ptt_ended", "users_update"):
                            pass
                except asyncio.TimeoutError:
                    continue
                except websockets.ConnectionClosed:
                    break
                except Exception as e:
                    break
        except Exception:
            pass
        finally:
            client.connected = False

    async def start_transmission(self, client: PTTClient) -> bool:
        if not client.joined or not client.ws:
            return False

        self.results["transmission_stats"]["total"] += 1
        try:
            await client.ws.send(json.dumps({"type": "ptt_start"}))
            
            start_time = time.time()
            while time.time() - start_time < 5.0:
                response = await asyncio.wait_for(client.ws.recv(), timeout=2.0)
                
                if isinstance(response, bytes):
                    client.audio_chunks_received += 1
                    self.results["audio_stats"]["chunks_received"] += 1
                    continue
                
                data = json.loads(response)
                msg_type = data.get("type")
                
                if msg_type == "ptt_granted":
                    client.is_transmitter = True
                    self.results["transmission_stats"]["successful"] += 1
                    return True
                elif msg_type == "ptt_denied":
                    self.results["transmission_stats"]["denied"] += 1
                    return False
                elif msg_type in ("ptt_started", "ptt_ended", "users_update", "pong"):
                    continue
            return False
        except Exception:
            self.results["transmission_stats"]["denied"] += 1
            return False

    async def stop_transmission(self, client: PTTClient):
        if not client.ws:
            return
        try:
            await client.ws.send(json.dumps({"type": "ptt_end"}))
            client.is_transmitter = False
        except Exception:
            pass

    async def send_audio_stream(self, client: PTTClient, duration: float = 2.0, chunk_size: int = 640):
        """Envía flujo de audio continuo durante un tiempo"""
        start_time = time.time()
        chunk_delay = chunk_size / 16000  # 16kHz sample rate
        
        while client.is_transmitter and (time.time() - start_time) < duration:
            try:
                chunk = self.generate_audio_chunk(chunk_size)
                await client.ws.send(chunk)
                client.audio_chunks_sent += 1
                self.results["audio_stats"]["chunks_sent"] += 1
                await asyncio.sleep(chunk_delay)
            except Exception:
                break

    async def disconnect_client(self, client: PTTClient):
        try:
            if client.ws:
                await client.ws.close()
        except Exception:
            pass
        client.connected = False
        client.joined = False

    async def run_test(self):
        print(f"\n{'='*70}")
        print(f"PRUEBA EXHAUSTIVA DE CARGA PTT")
        print(f"{'='*70}")
        print(f"Servidor: {self.server_url}")
        print(f"Canales: {self.num_channels}")
        print(f"Transmisores: {self.num_channels} (1 por canal)")
        print(f"Receptores: {self.num_channels * self.receivers_per_channel} ({self.receivers_per_channel} por canal)")
        print(f"Total clientes: {self.total_clients}")
        print(f"{'='*70}\n")

        self.results["start_time"] = datetime.now().isoformat()
        start = time.time()

        # Canales disponibles en el servidor
        available_channels = [
            "CANAL LIBRE", "Mantenimiento", "Trazabilidad", 
            "Produccion", "Calidad", "Logistica"
        ]
        
        # Canales para la prueba (repetir si es necesario)
        self.test_channels = []
        for i in range(self.num_channels):
            base_channel = available_channels[i % len(available_channels)]
            if i >= len(available_channels):
                self.test_channels.append(f"{base_channel}-{i//len(available_channels)+1}")
            else:
                self.test_channels.append(base_channel)
        
        print(f"[INFO] Canales de prueba: {self.test_channels[:5]}...")

        # Crear clientes
        print(f"[1/8] Creando {self.total_clients} clientes...")
        client_id = 1
        for i, channel in enumerate(self.test_channels):
            # Un transmisor por canal
            for t in range(self.transmitters_per_channel):
                client = PTTClient(
                    id=client_id,
                    username=f"TX-{channel}-{t+1}"
                )
                client.is_transmitter = True
                client.channel = channel
                self.clients.append(client)
                self.transmitters.append(client)
                client_id += 1
            
            # Receptores por canal
            self.receivers[channel] = []
            for r in range(self.receivers_per_channel):
                client = PTTClient(
                    id=client_id,
                    username=f"RX-{channel}-{r+1}"
                )
                client.channel = channel
                self.clients.append(client)
                self.receivers[channel].append(client)
                client_id += 1
        
        print(f"       -> {len(self.clients)} clientes creados")

        # Conectar todos los clientes
        print(f"[2/8] Conectando {len(self.clients)} clientes al servidor...")
        connect_tasks = [self.connect_client(c) for c in self.clients]
        results = await asyncio.gather(*connect_tasks, return_exceptions=True)
        connected = sum(1 for r in results if r is True)
        print(f"       -> Conectados: {connected}/{len(self.clients)}")

        # Join a canales
        print(f"[3/8] Uniando clientes a canales...")
        join_tasks = []
        for c in self.clients:
            if c.connected:
                join_tasks.append(self.join_channel(c, c.channel))
        
        join_results = await asyncio.gather(*join_tasks, return_exceptions=True)
        joined = sum(1 for r in join_results if r is True)
        print(f"       -> Unidos: {joined}/{len(self.clients)}")

        # Iniciar receptores de mensajes
        print(f"[4/8] Iniciando {len(self.clients)} receptores de mensajes...")
        stop_events = {c.id: asyncio.Event() for c in self.clients}
        receive_tasks = []
        for c in self.clients:
            if c.joined:
                task = asyncio.create_task(self.receive_messages(c, stop_events[c.id]))
                receive_tasks.append(task)
        
        print(f"       -> Receptores activos: {len(receive_tasks)}")

        # Iniciar transmisiones en todos los canales
        print(f"[5/8] Iniciando transmisiones simultáneas en {self.num_channels} canales...")
        
        async def transmit_on_channel(channel: str, transmitter: PTTClient):
            if not transmitter.joined:
                print(f"       [!] {transmitter.username} no pudo unirse")
                return False
            
            # Intentar obtener el canal
            granted = await self.start_transmission(transmitter)
            if granted:
                print(f"       [✓] {transmitter.username} transmite en {channel}")
                # Enviar audio por 2 segundos
                await self.send_audio_stream(transmitter, duration=2.0)
                await self.stop_transmission(transmitter)
                print(f"       [✓] {transmitter.username} terminó transmisión")
                return True
            else:
                print(f"       [!] {transmitter.username} no pudo transmitir")
                return False

        # Iniciar todas las transmisiones simultáneamente
        transmission_tasks = []
        for channel in self.test_channels:
            transmitters_in_channel = [t for t in self.transmitters if t.channel == channel]
            for tx in transmitters_in_channel:
                if tx.joined:
                    task = asyncio.create_task(transmit_on_channel(channel, tx))
                    transmission_tasks.append(task)

        # Esperar a que todas las transmisiones terminen
        if transmission_tasks:
            print(f"       -> {len(transmission_tasks)} transmisiones activas")
            await asyncio.gather(*transmission_tasks, return_exceptions=True)

        # Dejar que los receptores reciban el audio
        print(f"[6/8] Esperando recepción de audio...")
        await asyncio.sleep(2.0)

        # Detener receptores
        print(f"[7/8] Deteniendo receptores...")
        for c in self.clients:
            stop_events[c.id].set()
        
        await asyncio.sleep(0.5)

        # Desconectar clientes
        print(f"[8/8] Desconectando clientes...")
        disconnect_tasks = [self.disconnect_client(c) for c in self.clients]
        await asyncio.gather(*disconnect_tasks, return_exceptions=True)

        end = time.time()
        self.results["end_time"] = datetime.now().isoformat()
        self.results["duration_seconds"] = round(end - start, 2)

        # Estadísticas por canal
        for channel in self.test_channels:
            channel_receivers = self.receivers.get(channel, [])
            total_received = sum(r.audio_chunks_received for r in channel_receivers)
            channel_transmitters = [t for t in self.transmitters if t.channel == channel]
            total_sent = sum(t.audio_chunks_sent for t in channel_transmitters)
            
            self.results["channel_stats"][channel] = {
                "transmitters": len(channel_transmitters),
                "receivers": len(channel_receivers),
                "audio_sent": total_sent,
                "audio_received": total_received,
                "avg_per_receiver": round(total_received / len(channel_receivers), 2) if channel_receivers else 0,
            }

        # Determinar éxito
        success_rate = (self.results["join_stats"]["successful"] / self.results["join_stats"]["total"] * 100 
                       if self.results["join_stats"]["total"] > 0 else 0)
        self.results["success"] = (
            self.results["connection_stats"]["successful"] >= self.total_clients * 0.9 and
            success_rate >= 80 and
            self.results["transmission_stats"]["successful"] >= self.num_channels * 0.5
        )

        return self.results

    def print_summary(self):
        r = self.results
        print(f"\n{'='*70}")
        print(f"RESUMEN DE PRUEBA EXHAUSTIVA")
        print(f"{'='*70}")
        print(f"Servidor: {r['server_url']}")
        print(f"Duración: {r['duration_seconds']} segundos")
        print(f"\n--- Conexiones ---")
        print(f"Total:    {r['connection_stats']['total']}")
        print(f"Exitosas: {r['connection_stats']['successful']}")
        print(f"Fallidas: {r['connection_stats']['failed']}")
        print(f"\n--- Unirse a canales ---")
        print(f"Total:    {r['join_stats']['total']}")
        print(f"Exitosas: {r['join_stats']['successful']}")
        print(f"Fallidas: {r['join_stats']['failed']}")
        success_rate = (r['join_stats']['successful'] / r['join_stats']['total'] * 100 
                       if r['join_stats']['total'] > 0 else 0)
        print(f"Tasa:     {success_rate:.1f}%")
        print(f"\n--- Transmisiones PTT ---")
        print(f"Total:    {r['transmission_stats']['total']}")
        print(f"Concedidas: {r['transmission_stats']['successful']}")
        print(f"Denegadas: {r['transmission_stats']['denied']}")
        print(f"\n--- Audio ---")
        print(f"Chunks enviados:   {r['audio_stats']['chunks_sent']}")
        print(f"Chunks recibidos:  {r['audio_stats']['chunks_received']}")
        
        print(f"\n--- Estadísticas por Canal ---")
        for channel, stats in r['channel_stats'].items():
            print(f"  {channel}:")
            print(f"    Transmisión: {stats['audio_sent']} chunks")
            print(f"    Reception:   {stats['audio_received']} chunks ({stats['avg_per_receiver']} por receptor)")
        
        if r['errors']:
            print(f"\n--- Errores ---")
            for err in r['errors'][:5]:
                print(f"  - {err}")
        
        print(f"\n{'='*70}")
        print(f"RESULTADO: {'✓ EXITOSO' if r['success'] else '✗ FALLIDO'}")
        print(f"{'='*70}\n")

        return r['success']


async def main():
    import sys
    
    server_type = sys.argv[1] if len(sys.argv) > 1 else "python"
    num_channels = int(sys.argv[2]) if len(sys.argv) > 2 else 10
    receivers_per_channel = int(sys.argv[3]) if len(sys.argv) > 3 else 40
    
    if server_type == "go":
        server_url = "ws://localhost:8765"
    else:
        server_url = "ws://localhost:8765"
    
    tester = HeavyLoadTester(
        server_url, 
        num_channels=num_channels,
        transmitters_per_channel=1,
        receivers_per_channel=receivers_per_channel
    )
    
    try:
        results = await tester.run_test()
        tester.print_summary()
        
        output_file = f"test_results_{server_type}_heavy.json"
        with open(output_file, 'w') as f:
            json.dump(results, f, indent=2, default=str)
        print(f"Resultados guardados en: {output_file}")
        
        return 0 if results['success'] else 1
    except KeyboardInterrupt:
        print("\nPrueba interrumpida")
        return 1
    except Exception as e:
        print(f"\nError: {e}")
        import traceback
        traceback.print_exc()
        return 1


if __name__ == "__main__":
    exit(asyncio.run(main()))
