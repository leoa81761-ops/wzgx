import asyncio
import logging
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI, HTTPException, Request
from fastapi.exceptions import RequestValidationError
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import FileResponse, JSONResponse
from sqlalchemy.engine.url import make_url

from app.config import Settings, load_settings
from app.db import dispose_db, init_db, session_scope
from app.errors import AppError
from app.routers import admin, auth, studio
from app.seed import seed
from app.services.scheduler import scan_once

logger = logging.getLogger("gonghao")


def create_app(settings: Settings | None = None) -> FastAPI:
    settings = settings or load_settings()
    _ensure_sqlite_parent(settings.database_url)

    @asynccontextmanager
    async def lifespan(app: FastAPI):
        logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
        await init_db(settings.database_url)
        await seed(settings)
        task = None
        if settings.scheduler_enabled:
            task = asyncio.create_task(_scheduler_loop(settings))
        yield
        if task:
            task.cancel()
            try:
                await task
            except asyncio.CancelledError:
                pass
        await dispose_db()

    app = FastAPI(title="公号智写", lifespan=lifespan)
    app.state.settings = settings
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["http://localhost:5173", "http://127.0.0.1:5173"],
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )
    app.include_router(auth.router)
    app.include_router(studio.router)
    app.include_router(admin.router)

    @app.exception_handler(AppError)
    async def _app_error(_request: Request, exc: AppError):
        return JSONResponse(status_code=exc.status, content={"detail": exc.message})

    @app.exception_handler(RequestValidationError)
    async def _validation(_request: Request, _exc: RequestValidationError):
        return JSONResponse(status_code=422, content={"detail": "请求参数不正确"})

    @app.get("/api/health")
    async def health():
        from sqlalchemy import text

        async with session_scope() as session:
            await session.execute(text("SELECT 1"))
        return {"status": "ok"}

    @app.get("/")
    async def index():
        return _frontend_file("")

    @app.get("/{full_path:path}")
    async def spa(full_path: str):
        if full_path == "api" or full_path.startswith("api/"):
            raise HTTPException(status_code=404, detail="接口不存在")
        return _frontend_file(full_path)

    return app


def _frontend_file(full_path: str):
    root = _static_root()
    if root is None:
        return JSONResponse({"detail": "前端尚未构建"}, status_code=404)
    if full_path:
        candidate = (root / full_path).resolve()
        if candidate.is_file() and root in candidate.parents:
            return FileResponse(candidate)
    index = root / "index.html"
    if index.is_file():
        return FileResponse(index)
    return JSONResponse({"detail": "前端尚未构建"}, status_code=404)


def _static_root() -> Path | None:
    here = Path(__file__).resolve()
    candidates = [here.parents[1] / "static", here.parents[2] / "frontend" / "dist"]
    for path in candidates:
        if (path / "index.html").is_file():
            return path
    return None


def _ensure_sqlite_parent(url: str) -> None:
    try:
        database = make_url(url).database
    except Exception:
        return
    if not url.startswith("sqlite") or not database or database == ":memory:":
        return
    Path(database).parent.mkdir(parents=True, exist_ok=True)


async def _scheduler_loop(settings: Settings) -> None:
    while True:
        try:
            await scan_once(settings)
        except Exception:
            logger.exception("调度扫描失败")
        await asyncio.sleep(max(5, settings.scheduler_interval_seconds))


app = create_app()
