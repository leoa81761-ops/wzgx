from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from sqlalchemy import event
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker, create_async_engine

engine = None
SessionLocal: async_sessionmaker[AsyncSession] | None = None
dialect_name = "sqlite"


async def init_db(url: str) -> None:
    global engine, SessionLocal, dialect_name
    if engine is not None:
        await engine.dispose()
    dialect_name = "postgresql" if url.startswith("postgres") else "sqlite"
    kwargs = {"pool_pre_ping": True}
    if dialect_name == "sqlite":
        kwargs["connect_args"] = {"timeout": 30}
    engine = create_async_engine(url, **kwargs)

    @event.listens_for(engine.sync_engine, "connect")
    def _sqlite_fk(dbapi_connection, _connection_record):
        if dialect_name == "sqlite":
            cursor = dbapi_connection.cursor()
            cursor.execute("PRAGMA foreign_keys=ON")
            cursor.close()

    SessionLocal = async_sessionmaker(engine, expire_on_commit=False)
    from app.models import Base

    async with engine.begin() as conn:
        await conn.run_sync(Base.metadata.create_all)


async def dispose_db() -> None:
    global engine, SessionLocal
    if engine is not None:
        await engine.dispose()
    engine = None
    SessionLocal = None


@asynccontextmanager
async def session_scope() -> AsyncIterator[AsyncSession]:
    if SessionLocal is None:
        raise RuntimeError("数据库尚未初始化")
    session = SessionLocal()
    try:
        yield session
        await session.commit()
    except Exception:
        await session.rollback()
        raise
    finally:
        await session.close()
