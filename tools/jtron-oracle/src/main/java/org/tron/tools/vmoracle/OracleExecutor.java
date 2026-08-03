package org.tron.tools.vmoracle;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.math.BigInteger;
import org.bouncycastle.util.encoders.Hex;
import org.tron.common.runtime.InternalTransaction;
import org.tron.common.runtime.ProgramResult;
import org.tron.common.runtime.vm.DataWord;
import org.tron.common.runtime.vm.LogInfo;
import org.tron.core.vm.OperationRegistry;
import org.tron.core.vm.VM;
import org.tron.core.vm.config.VMConfig;
import org.tron.core.vm.program.Program;
import org.tron.core.vm.program.invoke.ProgramInvoke;
import org.tron.core.vm.program.invoke.ProgramInvokeImpl;
import org.tron.protos.Protocol.Transaction;

/** Executes one normalized request through java-tron's real Program and VM. */
final class OracleExecutor {

  private OracleExecutor() {
  }

  static OracleTypes.Execution execute(OracleTypes.Request request) throws Exception {
    validate(request);
    configureForks(request.world.version);

    byte[] owner = decodeAddress(request.tx.owner, "owner");
    byte[] contract = decodeAddress(request.tx.contract, "contract");
    byte[] code = accountCode(request.world, request.tx.contract);
    byte[] input = decodeHex(request.tx.data);
    byte[] witness = decodeOptionalAddress(request.world.block.witness);

    MemoryRepository repository = MemoryRepository.from(request.world);
    long balance = repository.proxy().getBalance(owner);
    long energyPrice = Math.max(100L, request.world.dynamicProps.energyFee);
    long balanceEnergy = Math.max(0L, (balance - request.tx.callValue) / energyPrice);
    long feeEnergy = request.tx.feeLimit <= 0 ? 0L : request.tx.feeLimit / energyPrice;
    long stakedEnergy = availableStakedEnergy(request.world, request.tx.owner);
    long energyLimit = Math.min(stakedEnergy + balanceEnergy, feeEnergy);

    long vmStart = System.nanoTime() / 1_000L;
    ProgramInvoke invoke = new ProgramInvokeImpl(
        contract,
        owner,
        owner,
        balance,
        request.tx.callValue,
        0L,
        0L,
        input,
        new byte[32],
        witness,
        request.world.block.timestamp / 1_000L,
        request.world.block.number,
        repository.proxy(),
        vmStart,
        vmStart + 60_000_000L,
        energyLimit);

    InternalTransaction internalTransaction = new InternalTransaction(
        Transaction.getDefaultInstance(), InternalTransaction.TrxType.TRX_UNKNOWN_TYPE);
    Program program = new Program(code, contract, invoke, internalTransaction);
    program.setRootTransactionId(rootTransactionID(request.tx.txID));
    if (VMConfig.allowTvmCompatibleEvm()) {
      program.setContractVersion(0);
    }
    VM.play(program, OperationRegistry.getTable());

    ProgramResult result = program.getResult();
    OracleTypes.Execution execution = new OracleTypes.Execution();
    execution.result = resultKind(result);
    execution.vmError = result.getException() == null ? "" : safeMessage(result.getException());
    execution.returnData = Hex.toHexString(result.getHReturn());
    execution.energyUsed = result.getEnergyUsed();
    execution.energyFee = Math.max(0L, result.getEnergyUsed() - stakedEnergy) * energyPrice;
    execution.originEnergyUsage = 0L;
    execution.storageWrites = failed(result)
        ? Collections.emptyMap() : repository.netStorageWrites();
    execution.logs = failed(result) ? Collections.emptyList() : logs(result.getLogInfoList());
    execution.createdAddress = "";
    return execution;
  }

  private static void validate(OracleTypes.Request request) {
    if (request == null || request.world == null || request.tx == null) {
      throw new IllegalArgumentException("request must contain world and tx");
    }
    if (!"TriggerSmartContract".equals(request.tx.type)) {
      throw new IllegalArgumentException("v0 supports TriggerSmartContract only");
    }
    if (request.world.dynamicProps == null) {
      request.world.dynamicProps = new OracleTypes.DynamicProps();
    }
    if (request.world.block == null) {
      request.world.block = new OracleTypes.Block();
    }
  }

  private static void configureForks(int version) {
    VMConfig.initAllowMultiSign(on(version, 9));
    VMConfig.initAllowTvmTransferTrc10(on(version, 6));
    VMConfig.initAllowTvmConstantinople(on(version, 8));
    VMConfig.initAllowTvmSolidity059(on(version, 9));
    VMConfig.initAllowShieldedTRC20Transaction(0);
    VMConfig.initAllowTvmIstanbul(on(version, 19));
    VMConfig.initAllowTvmFreeze(0);
    VMConfig.initAllowTvmVote(0);
    VMConfig.initAllowTvmLondon(on(version, 23));
    VMConfig.initAllowTvmCompatibleEvm(on(version, 23));
    VMConfig.initAllowHigherLimitForMaxCpuTimeOfOneTx(on(version, 24));
    VMConfig.initAllowTvmFreezeV2(0);
    VMConfig.initAllowOptimizedReturnValueOfChainId(0);
    VMConfig.initAllowDynamicEnergy(0);
    VMConfig.initAllowTvmShangHai(0);
    VMConfig.initAllowEnergyAdjustment(0);
    VMConfig.initAllowStrictMath(0);
    VMConfig.initAllowTvmCancun(0);
    VMConfig.initDisableJavaLangMath(0);
    VMConfig.initAllowTvmBlob(0);
    VMConfig.initAllowTvmSelfdestructRestriction(0);
  }

  private static long on(int version, int activation) {
    return version >= activation ? 1L : 0L;
  }

  private static String resultKind(ProgramResult result) {
    if (result.isRevert()) {
      return "REVERT";
    }
    RuntimeException error = result.getException();
    if (error == null) {
      return "SUCCESS";
    }
    String name = error.getClass().getSimpleName();
    if (name.contains("OutOfEnergy")) {
      return "OUT_OF_ENERGY";
    }
    if (name.contains("IllegalOperation") || name.contains("InvalidOpCode")) {
      return "ILLEGAL_OPERATION";
    }
    if (name.contains("BadJumpDestination")) {
      return "BAD_JUMP_DESTINATION";
    }
    if (name.contains("StackTooSmall")) {
      return "STACK_TOO_SMALL";
    }
    if (name.contains("StackTooLarge")) {
      return "STACK_TOO_LARGE";
    }
    if (name.contains("StaticCallModification")) {
      return "STATE_CHANGE_IN_STATIC";
    }
    return "FAULT";
  }

  private static boolean failed(ProgramResult result) {
    return result.isRevert() || result.getException() != null;
  }

  private static List<OracleTypes.LogEntry> logs(List<LogInfo> logs) {
    List<OracleTypes.LogEntry> output = new ArrayList<>();
    for (LogInfo log : logs) {
      OracleTypes.LogEntry entry = new OracleTypes.LogEntry();
      entry.address = "41" + Hex.toHexString(log.getAddress());
      entry.topics = new ArrayList<>();
      for (DataWord topic : log.getTopics()) {
        entry.topics.add(Hex.toHexString(topic.getData()));
      }
      entry.data = Hex.toHexString(log.getData());
      output.add(entry);
    }
    return output;
  }

  private static byte[] accountCode(OracleTypes.World world, String address) {
    OracleTypes.Account account = world.accounts == null ? null : world.accounts.get(address);
    if (account == null || account.code == null) {
      return new byte[0];
    }
    return decodeHex(account.code);
  }

  private static long availableStakedEnergy(OracleTypes.World world, String owner) {
    OracleTypes.Account account = world.accounts == null ? null : world.accounts.get(owner);
    if (account == null || account.energyStake <= 0
        || world.dynamicProps.totalEnergyWeight <= 0
        || world.dynamicProps.totalEnergyCurrentLimit <= 0) {
      return 0L;
    }
    BigInteger energy = BigInteger.valueOf(account.energyStake)
        .multiply(BigInteger.valueOf(world.dynamicProps.totalEnergyCurrentLimit))
        .divide(BigInteger.valueOf(world.dynamicProps.totalEnergyWeight));
    if (energy.compareTo(BigInteger.valueOf(Long.MAX_VALUE)) > 0) {
      return Long.MAX_VALUE;
    }
    return energy.longValue();
  }

  private static byte[] decodeAddress(String value, String field) {
    byte[] address = decodeHex(value);
    if (address.length != 21 || address[0] != 0x41) {
      throw new IllegalArgumentException(field + " must be a 21-byte 0x41 address");
    }
    return address;
  }

  private static byte[] decodeOptionalAddress(String value) {
    if (value == null || value.isEmpty()) {
      byte[] address = new byte[21];
      address[0] = 0x41;
      return address;
    }
    return decodeAddress(value, "witness");
  }

  private static byte[] decodeHex(String value) {
    if (value == null || value.isEmpty()) {
      return new byte[0];
    }
    String normalized = value.startsWith("0x") ? value.substring(2) : value;
    return Hex.decode(normalized);
  }

  private static byte[] rootTransactionID(String value) {
    byte[] txID = decodeHex(value);
    if (txID.length == 0) {
      return new byte[32];
    }
    if (txID.length != 32) {
      throw new IllegalArgumentException("txID must be 32 bytes");
    }
    return txID;
  }

  private static String safeMessage(Throwable error) {
    return error.getMessage() == null ? error.getClass().getSimpleName() : error.getMessage();
  }
}
