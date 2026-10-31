import asyncio
import json
import sys
import traceback

from pyrogram import Client
from pyrogram.errors import (
    FloodWait,
    PasswordHashInvalid,
    PhoneCodeExpired,
    PhoneCodeInvalid,
    PhoneNumberInvalid,
    PhoneNumberUnoccupied,
    SessionPasswordNeeded,
)


class PyroforkWorker:
    def __init__(self):
        self.client: Client | None = None
        self.api_id: int | None = None
        self.api_hash: str | None = None
        self.phone_number: str | None = None
        self.phone_code_hash: str | None = None

    async def ensure_connected(self):
        if self.client and not self.client.is_connected:
            await self.client.connect()

    async def handle_init(self, payload: dict) -> dict:
        self.api_id = int(payload["api_id"])
        self.api_hash = str(payload["api_hash"])
        if self.client:
            try:
                if self.client.is_connected:
                    await self.client.disconnect()
            except Exception:
                pass

        self.client = Client(
            name="pyrofork_worker_sess",
            api_id=self.api_id,
            api_hash=self.api_hash,
            in_memory=True,
        )
        return {"status": "ok"}

    async def handle_send_code(self, payload: dict) -> dict:
        if not self.client:
            return {"status": "error", "error": "Client not initialized"}

        self.phone_number = str(payload["phone_number"]).strip()
        try:
            await self.ensure_connected()
            sent_code = await self.client.send_code(self.phone_number)
            self.phone_code_hash = sent_code.phone_code_hash
            return {
                "status": "need_otp",
                "data": {
                    "phone_code_hash": self.phone_code_hash,
                    "type": str(sent_code.type),
                },
            }
        except PhoneNumberInvalid:
            return {"status": "error", "error": "Invalid phone number."}
        except PhoneNumberUnoccupied:
            return {"status": "error", "error": "This phone number is not registered on Telegram."}
        except FloodWait as e:
            return {"status": "error", "error": f"Flood wait error. Please wait {e.value} seconds before retrying."}
        except Exception as e:
            return {"status": "error", "error": f"Failed to send code: {str(e)}"}

    async def handle_start_qr(self, payload: dict) -> dict:
        return {
            "status": "error",
            "error": "QR code authentication is not supported by Pyrogram. Please use phone number login.",
        }

    async def handle_submit_otp(self, payload: dict) -> dict:
        if not self.client or not self.phone_number or not self.phone_code_hash:
            return {"status": "error", "error": "Missing active phone verification session"}

        phone_code = str(payload["phone_code"]).strip()
        try:
            await self.ensure_connected()
            await self.client.sign_in(
                phone_number=self.phone_number,
                phone_code_hash=self.phone_code_hash,
                phone_code=phone_code,
            )
            return {"status": "ok"}
        except SessionPasswordNeeded:
            return {"status": "need_password"}
        except PhoneCodeInvalid:
            return {"status": "error", "error": "Invalid verification code."}
        except PhoneCodeExpired:
            return {"status": "error", "error": "Verification code has expired. Please request a new one."}
        except FloodWait as e:
            return {"status": "error", "error": f"Flood wait error. Please wait {e.value} seconds before retrying."}
        except Exception as e:
            return {"status": "error", "error": f"Verification failed: {str(e)}"}

    async def handle_submit_password(self, payload: dict) -> dict:
        if not self.client:
            return {"status": "error", "error": "Client not initialized"}

        password = str(payload["password"])
        try:
            await self.ensure_connected()
            await self.client.check_password(password=password)
            return {"status": "ok"}
        except PasswordHashInvalid:
            return {"status": "error", "error": "Incorrect two-step verification password."}
        except FloodWait as e:
            return {"status": "error", "error": f"Flood wait error. Please wait {e.value} seconds."}
        except Exception as e:
            return {"status": "error", "error": f"2FA verification failed: {str(e)}"}

    async def handle_export_session(self, payload: dict) -> dict:
        if not self.client:
            return {"status": "error", "error": "Client not initialized"}

        try:
            session_str = await self.client.export_session_string()
            return {"status": "ok", "data": {"session": session_str}}
        except Exception as e:
            return {"status": "error", "error": f"Failed to export session: {str(e)}"}

    async def handle_validate_session(self, payload: dict) -> dict:
        session_str = str(payload["session"])
        val_client = Client(
            name="pyrofork_validation_sess",
            api_id=self.api_id,
            api_hash=self.api_hash,
            session_string=session_str,
            in_memory=True,
        )
        try:
            await val_client.connect()
            me = await val_client.get_me()
            return {
                "status": "ok",
                "data": {
                    "id": me.id,
                    "first_name": me.first_name or "",
                    "last_name": me.last_name or "",
                    "username": me.username or "",
                },
            }
        except Exception as e:
            return {"status": "error", "error": f"Session validation failed: {str(e)}"}
        finally:
            try:
                if val_client.is_connected:
                    await val_client.disconnect()
            except Exception:
                pass

    async def handle_cancel(self, payload: dict) -> dict:
        if self.client:
            try:
                if self.client.is_connected:
                    await self.client.disconnect()
            except Exception:
                pass
            self.client = None
        return {"status": "ok"}


async def main():
    worker = PyroforkWorker()

    while True:
        line = await asyncio.to_thread(sys.stdin.readline)
        if not line:
            break

        line_str = line.strip()
        if not line_str:
            continue

        try:
            req = json.loads(line_str)
            req_id = req.get("id", "")
            action = req.get("action", "")
            payload = req.get("payload") or {}

            if action == "init":
                res = await worker.handle_init(payload)
            elif action == "send_code":
                res = await worker.handle_send_code(payload)
            elif action == "start_qr":
                res = await worker.handle_start_qr(payload)
            elif action == "submit_otp":
                res = await worker.handle_submit_otp(payload)
            elif action == "submit_password":
                res = await worker.handle_submit_password(payload)
            elif action == "export_session":
                res = await worker.handle_export_session(payload)
            elif action == "validate_session":
                res = await worker.handle_validate_session(payload)
            elif action == "cancel":
                res = await worker.handle_cancel(payload)
                res["id"] = req_id
                sys.stdout.write(json.dumps(res) + "\n")
                sys.stdout.flush()
                break
            else:
                res = {"status": "error", "error": f"Unknown action: {action}"}

            res["id"] = req_id
            sys.stdout.write(json.dumps(res) + "\n")
            sys.stdout.flush()
        except Exception as e:
            sys.stderr.write(f"Worker exception: {traceback.format_exc()}\n")
            sys.stderr.flush()
            err_resp = {"id": req.get("id", "") if 'req' in locals() else "", "status": "error", "error": str(e)}
            sys.stdout.write(json.dumps(err_resp) + "\n")
            sys.stdout.flush()

    if worker.client:
        try:
            if worker.client.is_connected:
                await worker.client.disconnect()
        except Exception:
            pass


if __name__ == "__main__":
    asyncio.run(main())
