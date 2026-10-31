import asyncio
import json
import sys
import traceback

from telethon import TelegramClient, errors
from telethon.sessions import StringSession


class TelethonWorker:
    def __init__(self):
        self.client: TelegramClient | None = None
        self.api_id: int | None = None
        self.api_hash: str | None = None
        self.phone_number: str | None = None
        self.phone_code_hash: str | None = None
        self.qr_login_obj = None

    async def ensure_connected(self):
        if self.client and not self.client.is_connected():
            await self.client.connect()

    async def handle_init(self, payload: dict) -> dict:
        self.api_id = int(payload["api_id"])
        self.api_hash = str(payload["api_hash"])
        if self.client:
            try:
                if self.client.is_connected():
                    await self.client.disconnect()
            except Exception:
                pass

        self.client = TelegramClient(StringSession(), self.api_id, self.api_hash)
        return {"status": "ok"}

    async def handle_send_code(self, payload: dict) -> dict:
        if not self.client:
            return {"status": "error", "error": "Client not initialized"}

        self.phone_number = str(payload["phone_number"]).strip()
        try:
            await self.ensure_connected()
            sent_code = await self.client.send_code_request(self.phone_number)
            self.phone_code_hash = sent_code.phone_code_hash
            return {
                "status": "need_otp",
                "data": {
                    "phone_code_hash": self.phone_code_hash,
                    "type": type(sent_code.type).__name__,
                },
            }
        except errors.PhoneNumberInvalidError:
            return {"status": "error", "error": "Invalid phone number."}
        except errors.PhoneNumberUnoccupiedError:
            return {"status": "error", "error": "This phone number is not registered on Telegram."}
        except errors.FloodWaitError as e:
            return {"status": "error", "error": f"Flood wait error. Please wait {e.seconds} seconds before retrying."}
        except Exception as e:
            return {"status": "error", "error": f"Failed to send code: {str(e)}"}

    async def handle_start_qr(self, payload: dict) -> dict:
        if not self.client:
            return {"status": "error", "error": "Client not initialized"}

        try:
            await self.ensure_connected()
            self.qr_login_obj = await self.client.qr_login()
            expires = getattr(self.qr_login_obj, "expires", 300)
            if hasattr(expires, "timestamp"):
                expires_ts = int(expires.timestamp())
            else:
                expires_ts = int(expires) if expires else 300

            return {
                "status": "qr_code",
                "data": {
                    "token_url": self.qr_login_obj.url,
                    "expires": expires_ts,
                },
            }
        except Exception as e:
            return {"status": "error", "error": f"Failed to initialize QR login: {str(e)}"}

    async def handle_wait_qr(self, payload: dict) -> dict:
        if not self.qr_login_obj:
            return {"status": "error", "error": "QR login was not started"}

        try:
            user = await self.qr_login_obj.wait(timeout=180)
            return {
                "status": "ok",
                "data": {
                    "id": user.id,
                    "first_name": user.first_name or "",
                    "last_name": user.last_name or "",
                    "username": user.username or "",
                },
            }
        except errors.SessionPasswordNeededError:
            return {"status": "need_password"}
        except asyncio.TimeoutError:
            return {"status": "error", "error": "QR login timed out. Please request a new QR code."}
        except Exception as e:
            return {"status": "error", "error": f"QR authentication failed: {str(e)}"}

    async def handle_submit_otp(self, payload: dict) -> dict:
        if not self.client or not self.phone_number:
            return {"status": "error", "error": "Missing active phone verification session"}

        phone_code = str(payload["phone_code"]).strip()
        try:
            await self.ensure_connected()
            await self.client.sign_in(
                phone=self.phone_number,
                code=phone_code,
                phone_code_hash=self.phone_code_hash,
            )
            return {"status": "ok"}
        except errors.SessionPasswordNeededError:
            return {"status": "need_password"}
        except (errors.PhoneCodeInvalidError, errors.PhoneCodeEmptyError):
            return {"status": "error", "error": "Invalid verification code."}
        except errors.PhoneCodeExpiredError:
            return {"status": "error", "error": "Verification code has expired. Please request a new one."}
        except errors.FloodWaitError as e:
            return {"status": "error", "error": f"Flood wait error. Please wait {e.seconds} seconds."}
        except Exception as e:
            return {"status": "error", "error": f"Verification failed: {str(e)}"}

    async def handle_submit_password(self, payload: dict) -> dict:
        if not self.client:
            return {"status": "error", "error": "Client not initialized"}

        password = str(payload["password"])
        try:
            await self.ensure_connected()
            await self.client.sign_in(password=password)
            return {"status": "ok"}
        except (errors.PasswordHashInvalidError, errors.PasswordEmptyError):
            return {"status": "error", "error": "Incorrect two-step verification password."}
        except errors.FloodWaitError as e:
            return {"status": "error", "error": f"Flood wait error. Please wait {e.seconds} seconds."}
        except Exception as e:
            return {"status": "error", "error": f"2FA verification failed: {str(e)}"}

    async def handle_export_session(self, payload: dict) -> dict:
        if not self.client:
            return {"status": "error", "error": "Client not initialized"}

        try:
            session_str = self.client.session.save()
            return {"status": "ok", "data": {"session": session_str}}
        except Exception as e:
            return {"status": "error", "error": f"Failed to export session: {str(e)}"}

    async def handle_validate_session(self, payload: dict) -> dict:
        session_str = str(payload["session"])
        val_client = TelegramClient(StringSession(session_str), self.api_id, self.api_hash)
        try:
            await val_client.connect()
            if not await val_client.is_user_authorized():
                return {"status": "error", "error": "Client is not authorized with this session"}
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
                if val_client.is_connected():
                    await val_client.disconnect()
            except Exception:
                pass

    async def handle_cancel(self, payload: dict) -> dict:
        if self.client:
            try:
                if self.client.is_connected():
                    await self.client.disconnect()
            except Exception:
                pass
            self.client = None
        return {"status": "ok"}


async def main():
    worker = TelethonWorker()

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
            elif action == "wait_qr":
                res = await worker.handle_wait_qr(payload)
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
            if worker.client.is_connected():
                await worker.client.disconnect()
        except Exception:
            pass


if __name__ == "__main__":
    asyncio.run(main())
