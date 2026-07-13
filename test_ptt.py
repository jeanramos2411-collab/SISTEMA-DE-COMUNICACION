#!/usr/bin/env python3
"""
Script de prueba para simular 10 clientes conectados a un servidor PTT.
Prueba la conexión, join al canal, transmisión y recepción de audio.
"""

import asyncio
import json
import random
import string
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
    is_transmitting: bool = False
    session_id: Optional[str] = None
    last_message_time: float = field(default_factory=time.time)
    messages_received: int = 0
    audio_chunks_sent: int = 0
    errors: List[str] = field(default_factory=list)


class PTTLoadTester:
    def __init__(self, server_url: str, num_clients: int = 10, channel: str = "CANAL LIBRE"):
        self.server_url = server_url
        self.num_clients = num_clients
        self.channel = channel
        self.clients: List[PTTClient] = []
        self.results = {
            "server_url": server_url,
            "num_clients": num_clients,
            "channel": channel,
            "start_time": None,
            "end_time": None,
            "duration_seconds": 0,
            "connection_stats": {
                "total_attempts": 0,
                "successful": 0,
                "failed": 0,
            },
            "join_stats": {
                "total_attempts": 0,
                "successful": 0,
                "failed": 0,
            },
            "transmission_stats": {
                "total_transmissions": 0,
                "successful": 0,
                "denied": 0,
            },
            "audio_stats": {
                "total_chunks_sent": 0,
                "total_chunks_received": 0,
            },
            "client_details": [],
            "errors": [],
            "success": False,
        }

    def generate_audio_chunk(self, size: int = 640) -> bytes:
        """Genera un chunk de audio PCM mock (16-bit, 16kHz, mono)"""
        return bytes(random.getrandbits(8) for _ in range(size))

    async def connect_client(self, client: PTTClient) -> bool:
        """Conecta un cliente al servidor WebSocket"""
        self.results["connection_stats"]["total_attempts"] += 1
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
            self.results["errors"].append(f"Cliente {client.id}: Conexión fallida - {str(e)}")
            return False

    async def join_channel(self, client: PTTClient) -> bool:
        """Une un cliente al canal"""
        if not client.connected or not client.ws:
            return False

        self.results["join_stats"]["total_attempts"] += 1
        try:
            join_msg = {
                "type": "join",
                "username": client.username,
                "channel": self.channel,
                "device_id": f"test-device-{client.id}",
                "mac": f"00:11:22:33:44:{client.id:02x}"
            }
            await client.ws.send(json.dumps(join_msg))
            
            # Esperar respuesta
            response = await asyncio.wait_for(client.ws.recv(), timeout=5.0)
            data = json.loads(response)
            
            if data.get("type") == "joined":
                client.joined = True
                client.channel = self.channel
                client.session_id = data.get("session_id", "")
                self.results["join_stats"]["successful"] += 1
                return True
            elif data.get("type") == "approval_pending":
                # Canal requiere aprobación - esperamos que pase para la prueba
                client.joined = False
                self.results["join_stats"]["failed"] += 1
                self.results["errors"].append(f"Cliente {client.id}: Canal requiere aprobación")
                return False
            else:
                client.errors.append(f"Join: tipo inesperado {data.get('type')}")
                self.results["join_stats"]["failed"] += 1
                return False
        except asyncio.TimeoutError:
            client.errors.append("Join: timeout esperando respuesta")
            self.results["join_stats"]["failed"] += 1
            self.results["errors"].append(f"Cliente {client.id}: Timeout en join")
            return False
        except Exception as e:
            client.errors.append(f"Join: {str(e)}")
            self.results["join_stats"]["failed"] += 1
            self.results["errors"].append(f"Cliente {client.id}: Error en join - {str(e)}")
            return False

    async def receive_messages(self, client: PTTClient):
        """Recibe mensajes del servidor (en background)"""
        try:
            while client.connected and client.ws:
                try:
                    msg = await asyncio.wait_for(client.ws.recv(), timeout=1.0)
                    client.last_message_time = time.time()
                    
                    if isinstance(msg, bytes):
                        # Audio recibido
                        client.messages_received += 1
                        self.results["audio_stats"]["total_chunks_received"] += 1
                    else:
                        data = json.loads(msg)
                        msg_type = data.get("type")
                        
                        if msg_type == "ptt_started":
                            pass
                        elif msg_type == "ptt_ended":
                            pass
                        elif msg_type == "users_update":
                            pass
                        elif msg_type == "error":
                            client.errors.append(f"Server error: {data.get('message')}")
                        elif msg_type == "pong":
                            pass
                except asyncio.TimeoutError:
                    continue
                except websockets.ConnectionClosed:
                    break
                except Exception as e:
                    client.errors.append(f"Receive: {str(e)}")
                    break
        except Exception as e:
            client.errors.append(f"Receive loop: {str(e)}")
        finally:
            client.connected = False

    async def start_transmission(self, client: PTTClient) -> bool:
        """Intenta iniciar transmisión PTT"""
        if not client.joined or not client.ws:
            return False

        self.results["transmission_stats"]["total_transmissions"] += 1
        try:
            await client.ws.send(json.dumps({"type": "ptt_start"}))
            
            # Esperar respuesta - buscar específicamente ptt_granted o ptt_denied
            # Ignorar otros mensajes como ptt_started, ptt_ended, users_update, pong
            start_time = time.time()
            while time.time() - start_time < 5.0:
                response = await asyncio.wait_for(client.ws.recv(), timeout=2.0)
                
                if isinstance(response, bytes):
                    # Audio - continuar esperando
                    client.messages_received += 1
                    self.results["audio_stats"]["total_chunks_received"] += 1
                    continue
                
                data = json.loads(response)
                msg_type = data.get("type")
                
                if msg_type == "ptt_granted":
                    client.is_transmitting = True
                    self.results["transmission_stats"]["successful"] += 1
                    return True
                elif msg_type == "ptt_denied":
                    self.results["transmission_stats"]["denied"] += 1
                    return False
                elif msg_type in ("ptt_started", "ptt_ended", "users_update", "pong"):
                    # Ignorar mensajes de notificación
                    continue
                elif msg_type == "error":
                    client.errors.append(f"Server error: {data.get('message')}")
                    continue
                else:
                    # Otro mensaje - continuar
                    continue
            
            # Timeout esperando respuesta
            self.results["transmission_stats"]["denied"] += 1
            return False
        except asyncio.TimeoutError:
            self.results["transmission_stats"]["denied"] += 1
            return False
        except Exception as e:
            self.results["transmission_stats"]["denied"] += 1
            client.errors.append(f"PT Start: {str(e)}")
            return False

    async def stop_transmission(self, client: PTTClient):
        """Detiene transmisión PTT"""
        if not client.ws:
            return
        
        try:
            await client.ws.send(json.dumps({"type": "ptt_end"}))
            client.is_transmitting = False
        except Exception as e:
            client.errors.append(f"PT End: {str(e)}")

    async def send_audio_chunks(self, client: PTTClient, num_chunks: int = 5, delay: float = 0.05):
        """Envía chunks de audio mientras transmite"""
        for i in range(num_chunks):
            if not client.is_transmitting:
                break
            try:
                chunk = self.generate_audio_chunk()
                await client.ws.send(chunk)
                client.audio_chunks_sent += 1
                self.results["audio_stats"]["total_chunks_sent"] += 1
                await asyncio.sleep(delay)
            except Exception as e:
                client.errors.append(f"Audio send: {str(e)}")
                break

    async def disconnect_client(self, client: PTTClient):
        """Desconecta un cliente"""
        try:
            if client.ws:
                await client.ws.close()
        except Exception:
            pass
        finally:
            client.connected = False
            client.joined = False

    async def run_test(self):
        """Ejecuta la prueba completa"""
        print(f"\n{'='*60}")
        print(f"INICIANDO PRUEBA DE CARGA PTT")
        print(f"Servidor: {self.server_url}")
        print(f"Clientes: {self.num_clients}")
        print(f"Canal: {self.channel}")
        print(f"{'='*60}\n")

        self.results["start_time"] = datetime.now().isoformat()
        start = time.time()

        # Crear clientes
        print(f"[1/5] Creando {self.num_clients} clientes...")
        for i in range(self.num_clients):
            client = PTTClient(
                id=i + 1,
                username=f"TestUser{i+1:02d}"
            )
            self.clients.append(client)
        
        # Conectar clientes
        print(f"[2/5] Conectando clientes al servidor...")
        connect_tasks = [self.connect_client(c) for c in self.clients]
        results = await asyncio.gather(*connect_tasks)
        connected_count = sum(1 for r in results if r)
        print(f"       Conectados: {connected_count}/{self.num_clients}")

        # Join al canal
        print(f"[3/5] Uniendo clientes al canal...")
        join_tasks = [self.join_channel(c) for c in self.clients if c.connected]
        results = await asyncio.gather(*join_tasks)
        joined_count = sum(1 for r in results if r)
        print(f"       Unidos: {joined_count}/{connected_count}")

        # Iniciar receptor de mensajes para cada cliente
        print(f"[4/5] Iniciando receptores de mensajes...")
        receive_tasks = [
            asyncio.create_task(self.receive_messages(c)) 
            for c in self.clients if c.joined
        ]

        # Simular transmisiones entre clientes
        print(f"[5/5] Simulando transmisiones PTT...")
        joined_clients = [c for c in self.clients if c.joined]
        
        # Cada cliente transmite brevemente - solo uno a la vez
        for i, client in enumerate(joined_clients):
            print(f"       Cliente {client.id} ({client.username}) transmitiendo...")
            
            granted = await self.start_transmission(client)
            if granted:
                # Enviar algunos chunks de audio
                await self.send_audio_chunks(client, num_chunks=5, delay=0.05)
                await self.stop_transmission(client)
                print(f"       -> Transmisión completada ({client.audio_chunks_sent} chunks)")
            else:
                print(f"       -> Transmisión denegada (canal ocupado)")
            
            # Esperar a que el canal quede libre antes de siguiente transmisión
            await asyncio.sleep(0.5)

        # Dejar que los clientes reciban mensajes por un momento
        print(f"       Esperando recepción de mensajes...")
        await asyncio.sleep(1.0)

        # Detener receptores
        for task in receive_tasks:
            task.cancel()

        # Desconectar clientes
        print(f"       Desconectando clientes...")
        disconnect_tasks = [self.disconnect_client(c) for c in self.clients]
        await asyncio.gather(*disconnect_tasks, return_exceptions=True)

        end = time.time()
        self.results["end_time"] = datetime.now().isoformat()
        self.results["duration_seconds"] = round(end - start, 2)

        # Recopilar detalles de clientes
        for c in self.clients:
            self.results["client_details"].append({
                "id": c.id,
                "username": c.username,
                "connected": c.connected,
                "joined": c.joined,
                "channel": c.channel,
                "is_transmitting": c.is_transmitting,
                "session_id": c.session_id,
                "audio_chunks_sent": c.audio_chunks_sent,
                "messages_received": c.messages_received,
                "errors": c.errors,
            })

        # Determinar éxito general
        self.results["success"] = (
            self.results["connection_stats"]["successful"] == self.num_clients and
            self.results["join_stats"]["successful"] > 0 and
            len([e for e in self.results["errors"] if "error" in e.lower()]) < self.num_clients / 2
        )

        return self.results

    def print_summary(self):
        """Imprime un resumen de la prueba"""
        r = self.results
        print(f"\n{'='*60}")
        print(f"RESUMEN DE PRUEBA")
        print(f"{'='*60}")
        print(f"Servidor: {r['server_url']}")
        print(f"Duración: {r['duration_seconds']} segundos")
        print(f"\n--- Conexiones ---")
        print(f"Intentos: {r['connection_stats']['total_attempts']}")
        print(f"Exitosos: {r['connection_stats']['successful']}")
        print(f"Fallidos: {r['connection_stats']['failed']}")
        print(f"\n--- Unirse al canal ---")
        print(f"Intentos: {r['join_stats']['total_attempts']}")
        print(f"Exitosos: {r['join_stats']['successful']}")
        print(f"Fallidos: {r['join_stats']['failed']}")
        print(f"\n--- Transmisiones PTT ---")
        print(f"Total: {r['transmission_stats']['total_transmissions']}")
        print(f"Concedidas: {r['transmission_stats']['successful']}")
        print(f"Denegadas: {r['transmission_stats']['denied']}")
        print(f"\n--- Audio ---")
        print(f"Chunks enviados: {r['audio_stats']['total_chunks_sent']}")
        print(f"Chunks recibidos: {r['audio_stats']['total_chunks_received']}")
        
        if r['errors']:
            print(f"\n--- Errores ({len(r['errors'])}) ---")
            for err in r['errors'][:10]:
                print(f"  - {err}")
            if len(r['errors']) > 10:
                print(f"  ... y {len(r['errors']) - 10} errores más")
        
        print(f"\n{'='*60}")
        print(f"RESULTADO: {'✓ EXITOSO' if r['success'] else '✗ FALLIDO'}")
        print(f"{'='*60}\n")

        return r['success']


async def main():
    import sys
    
    # Determinar servidor y puerto
    server_type = sys.argv[1] if len(sys.argv) > 1 else "python"
    num_clients = int(sys.argv[2]) if len(sys.argv) > 2 else 10
    
    if server_type == "go":
        server_url = "ws://localhost:8765"
    else:
        server_url = "ws://localhost:8765"
    
    tester = PTTLoadTester(server_url, num_clients=num_clients)
    
    try:
        results = await tester.run_test()
        tester.print_summary()
        
        # Guardar resultados
        output_file = f"test_results_{server_type}.json"
        with open(output_file, 'w') as f:
            json.dump(results, f, indent=2, default=str)
        print(f"Resultados guardados en: {output_file}")
        
        return 0 if results['success'] else 1
    except KeyboardInterrupt:
        print("\nPrueba interrumpida por el usuario")
        return 1
    except Exception as e:
        print(f"\nError durante la prueba: {e}")
        import traceback
        traceback.print_exc()
        return 1


if __name__ == "__main__":
    exit(asyncio.run(main()))
