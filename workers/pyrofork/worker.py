import asyncio
import datetime
import json
import re
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

PHONE_REGEX = re.compile(r'\+?[1-9]\d{6,14}')
CODE_REGEX = re.compile(r'\b\d{4,6}\b')
SESSION_REGEX = re.compile(r'[A-Za-z0-9+/=_-]{40,}')
HEX_HASH_REGEX = re.compile(r'\b[a-fA-F0-9]{32,64}\b')


def sanitize_error(text: str) -> str:
    """Sanitizes sensitive authentication tokens, phone numbers, and hashes from error messages."""
    if not text:
        return ""
    text = SESSION_REGEX.sub("[REDACTED_SESSION]", text)
    text = HEX_HASH_REGEX.sub("[REDACTED_HASH]", text)
    text = PHONE_REGEX.sub("[REDACTED_PHONE]", text)
    text = CODE_REGEX.sub("[REDACTED_CODE]", text)
    return text


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
            return {"status": "error", "error": sanitize_error(f"Failed to send code: {str(e)}")}

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
            return {"status": "error", "error": sanitize_error(f"Verification failed: {str(e)}")}

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
            return {"status": "error", "error": sanitize_error(f"2FA verification failed: {str(e)}")}

    async def handle_export_session(self, payload: dict) -> dict:
        if not self.client:
            return {"status": "error", "error": "Client not initialized"}

        try:
            session_str = await self.client.export_session_string()
            return {"status": "ok", "data": {"session": session_str}}
        except Exception as e:
            return {"status": "error", "error": sanitize_error(f"Failed to export session: {str(e)}")}

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
            return {"status": "error", "error": sanitize_error(f"Session validation failed: {str(e)}")}
        finally:
            try:
                if val_client.is_connected:
                    await val_client.disconnect()
            except Exception:
                pass

    async def handle_get_me(self, payload: dict) -> dict:
        if not self.client:
            return {"status": "error", "error": "Client not initialized"}
        try:
            await self.ensure_connected()
            me = await self.client.get_me()
            if not me:
                return {"status": "error", "error": "Failed to retrieve account details"}
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
            return {"status": "error", "error": f"Failed to get account details: {str(e)}"}

    async def handle_send_to_saved_messages(self, payload: dict) -> dict:
        if not self.client:
            return {"status": "error", "error": "Client not initialized"}

        session_str = str(payload.get("session", "")).strip()
        provider_name = str(payload.get("provider_name", "Pyrogram")).strip()
        if not session_str:
            return {"status": "error", "error": "Session string cannot be empty"}

        try:
            await self.ensure_connected()
            me = await self.client.get_me()
            user_id = me.id if me else 0
            name = f"{me.first_name or ''} {me.last_name or ''}".strip() if me else "Authorized Account"
            username_str = f"@{me.username}" if (me and me.username) else "N/A"
            now_utc = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d %H:%M:%S UTC")

            text = (
                f"🔐 **Universal Telegram MTProto Session**\n\n"
                f"• **Provider:** {provider_name}\n"
                f"• **Account ID:** `{user_id}`\n"
                f"• **Account Name:** {name}\n"
                f"• **Username:** {username_str}\n"
                f"• **Generated At:** {now_utc}\n"
                f"• **Status:** ✅ Validated & Verified\n\n"
                f"📋 **Your String Session:**\n"
                f"```{session_str}```\n\n"
                f"⚠️ **Security Advisory:**\n"
                f"This String Session grants full access to your Telegram account. "
                f"Never share it with anyone or paste it into untrusted bots or websites."
            )

            # In Pyrogram, sending to "me" sends to Saved Messages (InputPeerSelf)
            sent_msg = await self.client.send_message("me", text)
            if not sent_msg or not getattr(sent_msg, "id", None):
                return {"status": "error", "error": "Telegram server did not return a valid message ID"}

            # Verify delivery by retrieving the message back from Saved Messages
            verify_msg = await self.client.get_messages("me", message_ids=sent_msg.id)
            if not verify_msg or getattr(verify_msg, "empty", False) or verify_msg.id != sent_msg.id:
                return {"status": "error", "error": "Delivery to Saved Messages could not be verified"}

            return {
                "status": "ok",
                "data": {
                    "message_id": sent_msg.id,
                },
            }
        except Exception as e:
            return {"status": "error", "error": sanitize_error(f"Failed to deliver session to Saved Messages: {str(e)}")}

    async def handle_cancel(self, payload: dict) -> dict:
        if self.client:
            try:
                if self.client.is_connected:
                    await self.client.disconnect()
            except Exception:
                pass
            self.client = None
        return {"status": "ok"}


MAX_LINE_LENGTH = 512 * 1024  # 512 KB IPC line limit

ALLOWED_ACTIONS = {
    "init",
    "send_code",
    "start_qr",
    "submit_otp",
    "submit_password",
    "export_session",
    "validate_session",
    "get_me",
    "send_to_saved_messages",
    "cancel",
}


def read_bounded_line(max_len: int = MAX_LINE_LENGTH) -> str | None:
    """Reads at most max_len+1 chars from sys.stdin to prevent memory DOS, draining overflow safely."""
    chunk = sys.stdin.readline(max_len + 1)
    if not chunk:
        return None
    if len(chunk) > max_len:
        # Drain rest of line until newline to maintain parser synchronization
        while True:
            extra = sys.stdin.readline(8192)
            if not extra or extra.endswith('\n'):
                break
        return "EXCEEDED_MAX_LENGTH"
    return chunk


async def main():
    worker = PyroforkWorker()

    while True:
        line = await asyncio.to_thread(read_bounded_line, MAX_LINE_LENGTH)
        if line is None:
            break

        if line == "EXCEEDED_MAX_LENGTH":
            sys.stderr.write("IPC message exceeds maximum permitted size\n")
            sys.stderr.flush()
            err_resp = {"id": "", "status": "error", "error": "IPC payload exceeds maximum allowed size"}
            sys.stdout.write(json.dumps(err_resp) + "\n")
            sys.stdout.flush()
            continue

        line_str = line.strip()
        if not line_str:
            continue

        try:
            req = json.loads(line_str)
        except json.JSONDecodeError as jde:
            sys.stderr.write(f"Invalid JSON received on stdin: {str(jde)}\n")
            sys.stderr.flush()
            err_resp = {"id": "", "status": "error", "error": "Malformed JSON request"}
            sys.stdout.write(json.dumps(err_resp) + "\n")
            sys.stdout.flush()
            continue

        req_id = req.get("id", "")
        action = req.get("action", "")
        payload = req.get("payload") or {}

        if action not in ALLOWED_ACTIONS:
            err_resp = {"id": req_id, "status": "error", "error": f"Unauthorized or unknown action: {action}"}
            sys.stdout.write(json.dumps(err_resp) + "\n")
            sys.stdout.flush()
            continue

        try:
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
            elif action == "get_me":
                res = await worker.handle_get_me(payload)
            elif action == "send_to_saved_messages":
                res = await worker.handle_send_to_saved_messages(payload)
            elif action == "cancel":
                res = await worker.handle_cancel(payload)
                res["id"] = req_id
                sys.stdout.write(json.dumps(res) + "\n")
                sys.stdout.flush()
                break

            res["id"] = req_id
            sys.stdout.write(json.dumps(res) + "\n")
            sys.stdout.flush()
        except Exception as e:
            sanitized_tb = sanitize_error(traceback.format_exc())
            sys.stderr.write(f"Worker exception during action {action}: {sanitized_tb}\n")
            sys.stderr.flush()
            err_resp = {"id": req_id, "status": "error", "error": sanitize_error(str(e))}
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
