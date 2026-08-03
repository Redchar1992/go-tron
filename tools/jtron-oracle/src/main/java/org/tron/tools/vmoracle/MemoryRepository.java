package org.tron.tools.vmoracle;

import com.google.protobuf.ByteString;
import java.lang.reflect.InvocationHandler;
import java.lang.reflect.Method;
import java.lang.reflect.Proxy;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.Map;
import java.util.Set;
import org.bouncycastle.util.encoders.Hex;
import org.tron.common.runtime.vm.DataWord;
import org.tron.core.capsule.AccountCapsule;
import org.tron.core.capsule.ContractCapsule;
import org.tron.core.vm.repository.Repository;
import org.tron.protos.Protocol.AccountType;
import org.tron.protos.contract.SmartContractOuterClass.SmartContract;

/** Isolated repository implementing the subset exercised by the TVM through a dynamic proxy. */
final class MemoryRepository implements InvocationHandler {

  private Map<String, AccountCapsule> accounts = new LinkedHashMap<>();
  private Map<String, byte[]> codes = new LinkedHashMap<>();
  private Map<String, ContractCapsule> contracts = new LinkedHashMap<>();
  private Map<String, Map<String, DataWord>> storage = new LinkedHashMap<>();
  private Set<String> writtenSlots = new LinkedHashSet<>();
  private Map<String, Map<String, DataWord>> inputStorage = new LinkedHashMap<>();
  private MemoryRepository parent;
  private Repository proxy;
  private long totalEnergyWeight;
  private long totalEnergyCurrentLimit;

  static MemoryRepository from(OracleTypes.World world) {
    MemoryRepository repository = new MemoryRepository();
    repository.totalEnergyWeight = world.dynamicProps.totalEnergyWeight;
    repository.totalEnergyCurrentLimit = world.dynamicProps.totalEnergyCurrentLimit;
    if (world.accounts != null) {
      world.accounts.forEach((addressHex, account) -> repository.seed(addressHex, account));
    }
    repository.inputStorage = copyStorage(repository.storage);
    repository.proxy();
    return repository;
  }

  Repository proxy() {
    if (proxy == null) {
      proxy = (Repository) Proxy.newProxyInstance(
          Repository.class.getClassLoader(), new Class<?>[]{Repository.class}, this);
    }
    return proxy;
  }

  Map<String, Map<String, String>> netStorageWrites() {
    Map<String, Map<String, String>> output = new LinkedHashMap<>();
    for (String composite : writtenSlots) {
      int separator = composite.indexOf(':');
      String address = composite.substring(0, separator);
      String slot = composite.substring(separator + 1);
      DataWord before = valueAt(inputStorage, address, slot);
      DataWord after = valueAt(storage, address, slot);
      if (Arrays.equals(wordBytes(before), wordBytes(after))) {
        continue;
      }
      output.computeIfAbsent(address, ignored -> new LinkedHashMap<>())
          .put(slot, Hex.toHexString(wordBytes(after)));
    }
    return output;
  }

  @Override
  public Object invoke(Object ignored, Method method, Object[] arguments) {
    Object[] args = arguments == null ? new Object[0] : arguments;
    switch (method.getName()) {
      case "getBalance":
        return balance((byte[]) args[0]);
      case "addBalance":
        return addBalance((byte[]) args[0], (Long) args[1]);
      case "getCode":
        return cloneBytes(codes.get(hex((byte[]) args[0])));
      case "saveCode":
        codes.put(hex((byte[]) args[0]), cloneBytes((byte[]) args[1]));
        return null;
      case "getStorageValue":
        return cloneWord(valueAt(storage, hex((byte[]) args[0]), wordHex((DataWord) args[1])));
      case "putStorageValue":
        putStorage((byte[]) args[0], (DataWord) args[1], (DataWord) args[2]);
        return null;
      case "getAccount":
        return accounts.get(hex((byte[]) args[0]));
      case "createAccount":
        return createAccount(args);
      case "createNormalAccount":
        return ensureAccount((byte[]) args[0], AccountType.Normal);
      case "updateAccount":
      case "putAccountValue":
        accounts.put(hex((byte[]) args[0]), (AccountCapsule) args[1]);
        return null;
      case "getContract":
        return contracts.get(hex((byte[]) args[0]));
      case "createContract":
      case "updateContract":
        contracts.put(hex((byte[]) args[0]), (ContractCapsule) args[1]);
        return null;
      case "deleteContract":
        deleteAccount((byte[]) args[0]);
        return null;
      case "newRepositoryChild":
        return child().proxy();
      case "commit":
        commit();
        return null;
      case "setParent":
        return null;
      case "getBlackHoleAddress":
        return blackHoleAddress();
      case "getTokenBalance":
      case "getAccountLeftEnergyFromFreeze":
      case "getAccountEnergyUsage":
      case "calculateGlobalEnergyLimit":
      case "getBeginCycle":
      case "getEndCycle":
      case "getHeadSlot":
      case "getSlotByTimestampMs":
      case "getTotalNetWeight":
      case "getTotalTronPowerWeight":
        return 0L;
      case "addTokenBalance":
        return (Long) args[2];
      case "getTotalEnergyWeight":
        return totalEnergyWeight;
      case "saveTotalEnergyWeight":
        totalEnergyWeight = (Long) args[0];
        return null;
      case "addTotalEnergyWeight":
        totalEnergyWeight += (Long) args[0];
        return null;
      case "toString":
        return "MemoryRepository";
      case "hashCode":
        return System.identityHashCode(this);
      case "equals":
        return ignored == args[0];
      default:
        return defaultValue(method.getReturnType());
    }
  }

  private void seed(String addressHex, OracleTypes.Account input) {
    byte[] address = Hex.decode(addressHex);
    AccountType type = input.code == null || input.code.isEmpty()
        ? AccountType.Normal : AccountType.Contract;
    AccountCapsule account = ensureAccount(address, type);
    account.setBalance(input.balance);
    if (input.code != null && !input.code.isEmpty()) {
      codes.put(addressHex, Hex.decode(input.code));
      SmartContract smartContract = SmartContract.newBuilder()
          .setContractAddress(ByteString.copyFrom(address))
          .setOriginAddress(ByteString.copyFrom(address))
          .setVersion(0)
          .build();
      contracts.put(addressHex, new ContractCapsule(smartContract));
    }
    if (input.storage != null) {
      input.storage.forEach((slot, value) -> storage
          .computeIfAbsent(addressHex, ignored -> new LinkedHashMap<>())
          .put(wordHex(slot), new DataWord(Hex.decode(value))));
    }
  }

  private AccountCapsule createAccount(Object[] args) {
    byte[] address = (byte[]) args[0];
    AccountType type = (AccountType) args[args.length - 1];
    return ensureAccount(address, type);
  }

  private AccountCapsule ensureAccount(byte[] address, AccountType type) {
    String key = hex(address);
    return accounts.computeIfAbsent(key,
        ignored -> new AccountCapsule(ByteString.copyFrom(address), type));
  }

  private long balance(byte[] address) {
    AccountCapsule account = accounts.get(hex(address));
    return account == null ? 0L : account.getBalance();
  }

  private long addBalance(byte[] address, long delta) {
    AccountCapsule account = ensureAccount(address, AccountType.Normal);
    account.setBalance(account.getBalance() + delta);
    return account.getBalance();
  }

  private void putStorage(byte[] addressBytes, DataWord key, DataWord value) {
    String address = hex(addressBytes);
    String slot = wordHex(key);
    storage.computeIfAbsent(address, ignored -> new LinkedHashMap<>())
        .put(slot, value.clone());
    writtenSlots.add(address + ":" + slot);
  }

  private void deleteAccount(byte[] addressBytes) {
    String address = hex(addressBytes);
    accounts.remove(address);
    codes.remove(address);
    contracts.remove(address);
    storage.remove(address);
  }

  private MemoryRepository child() {
    MemoryRepository child = new MemoryRepository();
    child.accounts = copyAccounts(accounts);
    child.codes = copyCodes(codes);
    child.contracts = new LinkedHashMap<>(contracts);
    child.storage = copyStorage(storage);
    child.inputStorage = copyStorage(inputStorage);
    child.writtenSlots = new LinkedHashSet<>(writtenSlots);
    child.totalEnergyWeight = totalEnergyWeight;
    child.totalEnergyCurrentLimit = totalEnergyCurrentLimit;
    child.parent = this;
    return child;
  }

  private void commit() {
    if (parent == null) {
      return;
    }
    parent.accounts = copyAccounts(accounts);
    parent.codes = copyCodes(codes);
    parent.contracts = new LinkedHashMap<>(contracts);
    parent.storage = copyStorage(storage);
    parent.writtenSlots = new LinkedHashSet<>(writtenSlots);
    parent.totalEnergyWeight = totalEnergyWeight;
    parent.totalEnergyCurrentLimit = totalEnergyCurrentLimit;
  }

  private static Map<String, AccountCapsule> copyAccounts(
      Map<String, AccountCapsule> source) {
    Map<String, AccountCapsule> copy = new LinkedHashMap<>();
    source.forEach((key, value) -> copy.put(key, new AccountCapsule(value.getData())));
    return copy;
  }

  private static Map<String, byte[]> copyCodes(Map<String, byte[]> source) {
    Map<String, byte[]> copy = new LinkedHashMap<>();
    source.forEach((key, value) -> copy.put(key, cloneBytes(value)));
    return copy;
  }

  private static Map<String, Map<String, DataWord>> copyStorage(
      Map<String, Map<String, DataWord>> source) {
    Map<String, Map<String, DataWord>> copy = new LinkedHashMap<>();
    source.forEach((address, slots) -> {
      Map<String, DataWord> slotCopy = new LinkedHashMap<>();
      slots.forEach((slot, value) -> slotCopy.put(slot, cloneWord(value)));
      copy.put(address, slotCopy);
    });
    return copy;
  }

  private static DataWord valueAt(
      Map<String, Map<String, DataWord>> source, String address, String slot) {
    Map<String, DataWord> slots = source.get(address);
    return slots == null ? null : slots.get(slot);
  }

  private static String wordHex(String value) {
    return wordHex(new DataWord(Hex.decode(value)));
  }

  private static String wordHex(DataWord value) {
    return Hex.toHexString(value.getData());
  }

  private static String hex(byte[] value) {
    return Hex.toHexString(value);
  }

  private static byte[] wordBytes(DataWord value) {
    return value == null ? new byte[32] : value.getData();
  }

  private static DataWord cloneWord(DataWord value) {
    return value == null ? null : value.clone();
  }

  private static byte[] cloneBytes(byte[] value) {
    return value == null ? null : value.clone();
  }

  private static byte[] blackHoleAddress() {
    byte[] address = new byte[21];
    address[0] = 0x41;
    return address;
  }

  private static Object defaultValue(Class<?> type) {
    if (!type.isPrimitive()) {
      return null;
    }
    if (type == boolean.class) {
      return false;
    }
    if (type == byte.class) {
      return (byte) 0;
    }
    if (type == short.class) {
      return (short) 0;
    }
    if (type == int.class) {
      return 0;
    }
    if (type == long.class) {
      return 0L;
    }
    if (type == float.class) {
      return 0F;
    }
    if (type == double.class) {
      return 0D;
    }
    if (type == char.class) {
      return '\0';
    }
    return null;
  }
}
